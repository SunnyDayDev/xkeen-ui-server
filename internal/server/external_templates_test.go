package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type extTemplateFixture struct {
	calls    int
	snapshot *templates.Snapshot
}

func (f *extTemplateFixture) CreateExternalTemplate(context.Context, externalaccess.Permit, string) (templates.Summary, error) {
	panic("unexpected create")
}

func (f *extTemplateFixture) ReadExternalTemplate(_ context.Context, p externalaccess.Permit, id string) (*templates.Snapshot, error) {
	f.calls++
	if p.Version != 4 {
		return nil, externalaccess.ErrUnauthorized
	}
	if id != f.snapshot.ID {
		return nil, templates.ErrNotFound
	}
	return f.snapshot, nil
}
func TestExternalDraftWire(t *testing.T) {
	raw := "{ \"key\" : 9007199254740993 }"
	element := configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440000", Content: raw, Variables: []configuration.VariableDefinition{{Name: "one", Type: "string"}, {Name: "two", Type: "number", Default: json.RawMessage("9007199254740993")}}}
	f := &extTemplateFixture{snapshot: &templates.Snapshot{TemplateDraft: configuration.TemplateDraft{ID: "space +", Sections: []configuration.TemplateSection{{Name: "rules", Elements: []configuration.TemplateElement{{Element: element, Disabled: true}}}}}, Version: 9007199254740993}}
	handler := NewExternalTemplates(f)
	for _, tc := range []struct {
		query  string
		p      int64
		status int
	}{{"templateId=space+%2B", 4, 200}, {"templateId=Space+%2B", 4, 404}, {"templateId=space+%2B", 5, 401}, {"templateId=a&templateId=b", 4, 400}, {"other=a", 4, 400}, {"templateId=%FF", 4, 400}, {"templateId=%", 4, 400}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/templates/item?"+tc.query, nil)
		handler(w, r, externalaccess.Permit{Version: tc.p})
		if w.Code != tc.status {
			t.Errorf("got %d want %d", w.Code, tc.status)
		}
		if tc.status != 200 {
			if w.Header().Get("ETag") != "" || bytes.Contains(w.Body.Bytes(), []byte("9007199254740993")) {
				t.Fatal("data in error")
			}
			continue
		}
		if w.Header().Get("ETag") != fmt.Sprintf("\"%x\"", sha256.Sum256(w.Body.Bytes())) || w.Header().Get("Cache-Control") != "no-store, no-transform" {
			t.Fatal("unstable representation")
		}
		var result struct {
			Version  string
			Sections []struct {
				Elements []struct {
					Source    string
					Disabled  bool
					Variables []struct{ DefaultSource *string }
				}
			}
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Version != "9007199254740993" || result.Sections[0].Elements[0].Source != raw || !result.Sections[0].Elements[0].Disabled || result.Sections[0].Elements[0].Variables[0].DefaultSource != nil || *result.Sections[0].Elements[0].Variables[1].DefaultSource != "9007199254740993" {
			t.Fatal("authored data not retained")
		}
	}
}

func TestExternalPatchRequiresCondition(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/v1/templates/item?templateId=example", nil)
	NewExternalTemplates(&extTemplateFixture{})(w, r, externalaccess.Permit{Version: 4})
	if w.Code != 428 {
		t.Fatalf("missing If-Match: got %d, want 428", w.Code)
	}
}

func (f *extTemplateFixture) ReplaceExternalTemplate(ctx context.Context, p externalaccess.Permit, draft configuration.TemplateDraft, expected int64) (int64, error) {
	if p.Version != 4 {
		return 0, externalaccess.ErrUnauthorized
	}
	if expected != f.snapshot.Version {
		return 0, templates.ErrVersionConflict
	}
	f.snapshot = &templates.Snapshot{TemplateDraft: draft, Version: expected + 1}
	return expected + 1, nil
}

func (f *extTemplateFixture) ListExternalTemplates(context.Context, externalaccess.Permit) ([]templates.Summary, error) {
	return nil, nil
}
