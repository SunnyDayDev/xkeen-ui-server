//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestProcessAgentReportsPublishedApplications(t *testing.T) {
	if os.Getenv("XKEEN_TEST_REPORTS") != "1" {
		t.Skip("isolated application report lab required")
	}
	checkControlledApplication(t, "report")
}

type reportDeliveryFault struct {
	drop, unavailable              atomic.Bool
	mu                             sync.Mutex
	accepted                       [][]byte
	conflict                       atomic.Bool
	conflicts, heartbeats, watches atomic.Int64
}

func (f *reportDeliveryFault) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/v1/watch" {
			f.watches.Add(1)
		}
		if r.URL.Path != "/agent/v1/contact" || r.Method != http.MethodPost || r.ContentLength <= 0 {
			if r.URL.Path == "/agent/v1/contact" && r.Method == http.MethodPost {
				f.heartbeats.Add(1)
			}
			next.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2048))
		if err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if f.unavailable.Load() {
			unavailable(w)
			return
		}
		if f.conflict.Load() {
			f.conflicts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"report_conflict"}`)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		response := httptest.NewRecorder()
		next.ServeHTTP(response, r)
		if response.Code == http.StatusNoContent {
			f.mu.Lock()
			f.accepted = append(f.accepted, raw)
			f.mu.Unlock()
			if f.drop.Load() {
				connection, _, err := http.NewResponseController(w).Hijack()
				if err == nil {
					_ = connection.Close()
					return
				}
			}
		}
		for header, values := range response.Header() {
			w.Header()[header] = values
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	})
}

func (f *reportDeliveryFault) bodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.accepted...)
}

type processPublicationValidator struct{ xray, dir string }

func (v processPublicationValidator) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	if profile != configuration.XrayIdentityVersion {
		return publication.ErrCatalogRejected
	}
	dir, err := os.MkdirTemp(v.dir, "central-check-")
	if err != nil {
		return publication.ErrValidatorUnavailable
	}
	defer os.RemoveAll(dir)
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.Name), file.Content, 0600); err != nil {
			return publication.ErrValidatorUnavailable
		}
	}
	cmd := exec.CommandContext(ctx, v.xray, "run", "-test", "-confdir", dir)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "XRAY_LOCATION_ASSET=/opt/etc/xray/dat"}
	if err := cmd.Run(); err != nil {
		return publication.ErrCatalogRejected
	}
	return nil
}

// publicationDraft reconstructs synthetic operator inputs. The acceptance
// goes through C06 and subsequently checks those actual published bytes.
func publicationDraft(t *testing.T, router, template string, files []configuration.CatalogFile) routersettings.Draft {
	t.Helper()
	draft := routersettings.Draft{ID: router, TemplateID: template, PersonalOrder: []string{}}
	add := func(group string, items ...json.RawMessage) {
		section := routersettings.Group{Name: group}
		for i, item := range items {
			id := fmt.Sprintf("550e8400-e29b-41d4-a716-%012d", len(draft.Groups)*100+i+1)
			section.Local = append(section.Local, routersettings.LocalElement{ID: id, Content: item})
			if group == "rules" {
				draft.PersonalOrder = append(draft.PersonalOrder, id)
			}
		}
		draft.Groups = append(draft.Groups, section)
	}
	for _, file := range files {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(file.Content, &document); err != nil {
			t.Fatal(err)
		}
		switch file.Name {
		case "01_log.json":
			add("log", document["log"])
		case "02_api.json":
			add("api", document["api"])
			add("stats", document["stats"])
		case "03_inbounds.json", "04_outbounds.json":
			group := "inbounds"
			if file.Name == "04_outbounds.json" {
				group = "outbounds"
			}
			var groupItems []json.RawMessage
			if err := json.Unmarshal(document[group], &groupItems); err != nil {
				t.Fatal(err)
			}
			add(group, groupItems...)
		case "05_routing.json":
			var routing map[string]json.RawMessage
			if err := json.Unmarshal(document["routing"], &routing); err != nil {
				t.Fatal(err)
			}
			var rules []json.RawMessage
			if err := json.Unmarshal(routing["rules"], &rules); err != nil {
				t.Fatal(err)
			}
			delete(routing, "rules")
			fields, err := json.Marshal(routing)
			if err != nil {
				t.Fatal(err)
			}
			add("routing", fields)
			add("rules", rules...)
		default:
			t.Fatal("unexpected application fixture section")
		}
	}
	return draft
}
