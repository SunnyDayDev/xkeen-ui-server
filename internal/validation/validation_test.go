package validation

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func sample() []configuration.CatalogFile {
	var result []configuration.CatalogFile
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		result = append(result, configuration.CatalogFile{Name: name, Content: []byte("{}\n")})
	}
	return result
}

func TestAttempt(t *testing.T) {
	files := sample()
	req, err := newRequest(configuration.XrayIdentityVersion, files)
	if err != nil {
		t.Fatal(err)
	}
	files[0].Content[0] = ' '
	if req.Files[0].Bytes[0] != '{' {
		t.Fatal("aliased input")
	}
	reordered := sample()
	reordered[0], reordered[2] = reordered[2], reordered[0]
	other, err := newRequest(req.Profile, reordered)
	if err != nil || req.Digest != other.Digest || req.Attempt == other.Attempt {
		t.Fatal("attempt identity")
	}
	changed, _ := newRequest(req.Profile, files)
	if req.Digest == changed.Digest {
		t.Fatal("changed bytes")
	}
	for _, invalid := range [][]configuration.CatalogFile{files[:4], append(files, files[0]), append(files, configuration.CatalogFile{Name: "../extra.json"})} {
		if _, err := newRequest(req.Profile, invalid); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}
func TestClientBindsResponse(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want error
	}{{"ok", nil}, {"reject", publication.ErrCatalogRejected}, {"timeout", publication.ErrValidatorUnavailable}, {"wrong-attempt", publication.ErrValidatorUnavailable}, {"wrong-profile", publication.ErrValidatorUnavailable}, {"wrong-digest", publication.ErrValidatorUnavailable}, {"redirect", publication.ErrValidatorUnavailable}} {
		t.Run(tt.mode, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req request
				if !decode(w, r, &req, maxBody) {
					return
				}
				resp := req.reply("ok")
				switch tt.mode {
				case "reject":
					resp.Outcome = "rejected"
				case "timeout":
					resp.Outcome = "unavailable"
				case "wrong-attempt":
					resp.Attempt = "other"
				case "wrong-profile":
					resp.Profile = "other"
				case "wrong-digest":
					resp.Digest = "other"
				case "redirect":
					http.Redirect(w, r, "/other", 307)
					return
				}
				send(w, resp)
			})
			srv := httptest.NewServer(h)
			defer srv.Close()
			c := client{url: srv.URL, http: &http.Client{CheckRedirect: noRedirect}}
			err := c.Validate(context.Background(), configuration.XrayIdentityVersion, sample())
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}
func TestTransportRequiresExplicitTrust(t *testing.T) {
	if _, err := Remote("http://validator.example", &tls.Config{}); err == nil {
		t.Fatal("plain HTTP accepted")
	}
	if _, err := Remote("https://validator.example", &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("unverified peer accepted")
	}
}
func TestWorkerEnvironmentFailures(t *testing.T) {
	for _, tc := range []struct {
		state dockerState
		want  error
	}{{dockerState{Status: "exited", ExitCode: 23}, publication.ErrCatalogRejected}, {dockerState{Status: "exited", ExitCode: 23, OOMKilled: true}, publication.ErrValidatorUnavailable}, {dockerState{Status: "exited", ExitCode: 125}, publication.ErrValidatorUnavailable}, {dockerState{Status: "exited", ExitCode: 0}, nil}} {
		if got := classify(tc.state); !errors.Is(got, tc.want) {
			t.Fatalf("got %v want %v", got, tc.want)
		}
	}
}
