package localvalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

type syntheticApply func(context.Context, string, string) (restart.Confirmed, error)

func (f syntheticApply) Apply(ctx context.Context, key, tag string) (restart.Confirmed, error) {
	return f(ctx, key, tag)
}

func TestApplicationHandlerPassesOnlyBoundCredentialAndTag(t *testing.T) {
	scope := preparation.Scope{Origin: "https://controller.example", RouterID: "router-example", Profile: "26.3.27", VerifierID: "fixture"}
	key := agentcredential.Generate(agentcredential.Working).Encode()
	input := applicationRequest{Binding: binding{Role: applicationRole, Action: "apply", Scope: scope,
		Tag: `"v1-` + strings.Repeat("a", 64) + `"`, Nonce: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Credential: key}
	calls := 0
	handler := ApplicationHandler(scope, syntheticApply(func(_ context.Context, got, tag string) (restart.Confirmed, error) {
		calls++
		if got != key || tag != input.Binding.Tag {
			t.Fatal("application call was not bound to the request")
		}
		return restart.Confirmed{}, nil
	}))
	send := func(req applicationRequest) *httptest.ResponseRecorder {
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, applicationEndpoint, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		return reply
	}
	if reply := send(input); reply.Code != http.StatusOK || strings.Contains(reply.Body.String(), key) || calls != 1 {
		t.Fatal("expected an accepted response without credential contents")
	}
	input.Binding.Scope.RouterID = "elsewhere-example"
	if reply := send(input); reply.Code != http.StatusBadRequest || calls != 1 {
		t.Fatal("a different scope reached the application")
	}
}

func TestReportProfileKeepsApplyVersionAndReportOnlySeparate(t *testing.T) {
	scope := preparation.Scope{Origin: "https://controller.example", RouterID: "router-example", Profile: "26.3.27", VerifierID: "fixture"}
	key := agentcredential.Generate(agentcredential.Working).Encode()
	tag := `"v1-` + strings.Repeat("a", 64) + `"`
	applyCalls, reportCalls := 0, 0
	apply := syntheticApply(func(context.Context, string, string) (restart.Confirmed, error) {
		applyCalls++
		return restart.Confirmed{}, nil
	})
	report := func(context.Context, string) (bool, error) {
		reportCalls++
		return true, nil
	}
	send := func(handler http.Handler, endpoint, action, desired string) *httptest.ResponseRecorder {
		t.Helper()
		request := applicationRequest{Binding: binding{Role: applicationRole, Action: action, Scope: scope,
			Tag: desired, Nonce: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Credential: key}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		httpRequest := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		httpRequest.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, httpRequest)
		return reply
	}
	legacy := ApplicationHandler(scope, apply)
	if got := send(legacy, applicationReportEndpoint, "report_v1", ""); got.Code != 404 {
		t.Fatal("legacy profile exposed reporting")
	}
	current := ApplicationProfileHandler(scope, apply, report)
	if got := send(current, applicationEndpoint, "apply", tag); got.Code != 400 {
		t.Fatal("legacy application action reached enabled supervisor")
	}
	if got := send(current, applicationEndpoint, "apply_report_v1", tag); got.Code != 200 {
		t.Fatal("enabled client could not request controlled application")
	}
	reportOnly := ApplicationProfileHandler(scope, nil, report)
	if got := send(reportOnly, applicationEndpoint, "apply_report_v1", tag); got.Code != 404 {
		t.Fatal("report-only endpoint exposes apply")
	}
	if got := send(reportOnly, applicationReportEndpoint, "report_v1", ""); got.Code != 200 ||
		!strings.Contains(got.Body.String(), "reported") || strings.Contains(got.Body.String(), key) ||
		applyCalls != 1 || reportCalls != 1 {
		t.Fatal("report-only endpoint did not bind the operation")
	}
}
