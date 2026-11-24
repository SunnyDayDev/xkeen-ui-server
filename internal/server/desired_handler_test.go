package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type desiredReaderFunc func(context.Context, string) (*publication.Revision, error)

func (f desiredReaderFunc) ReadDesired(ctx context.Context, id string) (*publication.Revision, error) {
	return f(ctx, id)
}

func TestAgentDesiredScopesAndCanonicalizesConditionalRead(t *testing.T) {
	stub := &stubAgentAuthenticator{t: t, key: agentcredential.Generate(agentcredential.Working).Encode()}
	var rev = &publication.Revision{Number: 8, Catalog: publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion}}
	for _, name := range []string{"05_routing.json", "03_inbounds.json", "02_api.json", "01_log.json", "04_outbounds.json"} {
		rev.Catalog.Files = append(rev.Catalog.Files, configuration.CatalogFile{Name: name, Content: []byte(" {}\n")})
	}
	var calls int
	var storeErr error
	handler := NewAgentHandler(stub, desiredReaderFunc(func(_ context.Context, id string) (*publication.Revision, error) {
		if id != "router-example" {
			t.Fatal("desired was read outside the authenticated scope")
		}
		calls++
		return rev, storeErr
	}))
	get := func(token, path string, conditions ...string) *httptest.ResponseRecorder {
		req := agentRequest(http.MethodGet, path, token, "")
		for _, condition := range conditions {
			req.Header.Add("If-None-Match", condition)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	const path = "/agent/v1/desired"
	first := get(stub.key, path)
	tag := first.Header().Get("ETag")
	if first.Code != 200 || !agentrevision.ValidTag(tag) || !strings.Contains(first.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("complete response=%d %v", first.Code, first.Header())
	}
	slices.Reverse(rev.Catalog.Files)
	reordered := get(stub.key, path)
	if reordered.Code != 200 || reordered.Header().Get("ETag") != tag || !bytes.Equal(first.Body.Bytes(), reordered.Body.Bytes()) {
		t.Fatal("reordering storage representation changes strong response")
	}
	cached := get(stub.key, path, tag)
	if cached.Code != 304 || cached.Header().Get("ETag") != tag || cached.Body.Len() != 0 {
		t.Fatalf("matching cache = %d, %v", cached.Code, cached.Header())
	}
	foreign, _, err := agentrevision.Encode("other.example", agentrevision.Snapshot{
		Number: rev.Number, Profile: rev.Catalog.Profile, Files: rev.Catalog.Files,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, conditions := range map[string][]string{
		"wildcard": {"*"}, "weak": {"W/" + tag}, "malformed": {`"unclosed`},
		"duplicate": {tag, tag}, "list": {tag + ", " + foreign}, "foreign": {foreign},
	} {
		t.Run(name, func(t *testing.T) {
			if got := get(stub.key, path, conditions...); got.Code != 200 || got.Header().Get("ETag") != tag {
				t.Fatalf("condition = %d", got.Code)
			}
		})
	}
	beforeDenied := calls
	stub.err = agentcredentials.ErrUnauthorized
	if got := get(stub.key, path, tag); got.Code != 401 || calls != beforeDenied || got.Header().Get("ETag") != "" {
		t.Fatalf("unauthorized conditional = %d, reads=%d", got.Code, calls)
	}
	stub.err = nil
	if got := get(stub.key, path+"?routerId=other", tag); got.Code != 400 || calls != beforeDenied {
		t.Fatalf("explicit scope = %d, reads=%d", got.Code, calls)
	}
	rev = nil
	if got := get(stub.key, path, tag); got.Code != 204 || got.Body.Len() != 0 || got.Header().Get("ETag") != "" {
		t.Fatalf("no desired = %d", got.Code)
	}
	storeErr = errors.New("private store failure")
	if got := get(stub.key, path); got.Code != 503 || strings.Contains(got.Body.String(), "private") {
		t.Fatalf("desired from failed storage = %d", got.Code)
	}
}
