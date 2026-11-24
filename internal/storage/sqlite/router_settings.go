package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

// RouterSettings owns the SQLite representation of one complete router draft.
type RouterSettings struct {
	db *sql.DB
}

func NewRouterSettings(db *sql.DB) *RouterSettings {
	return &RouterSettings{db: db}
}

func (r *RouterSettings) Save(ctx context.Context, draft routersettings.Draft) error {
	snapshot, err := json.Marshal(encodeRouterDraft(draft))
	if err != nil {
		return errors.New("encode router draft")
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO router_settings (id, snapshot) VALUES (?, ?)
		ON CONFLICT (id) DO UPDATE SET snapshot = excluded.snapshot`, draft.ID, snapshot)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("write router draft")
	}
	return nil
}

func (r *RouterSettings) Get(ctx context.Context, id string) (routersettings.Draft, bool, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, "SELECT snapshot FROM router_settings WHERE id = ?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return routersettings.Draft{}, false, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return routersettings.Draft{}, false, ctx.Err()
		}
		return routersettings.Draft{}, false, errors.New("read router draft")
	}
	if _, issue := jsonvalue.Parse(raw); issue != nil {
		return routersettings.Draft{}, false, routersettings.ErrStoredDraft
	}
	var stored storedRouterDraft
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return routersettings.Draft{}, false, routersettings.ErrStoredDraft
	}
	return stored.draft(id), true, nil
}

// Store raw JSON as bytes. Embedding RawMessage would compact it and may rewrite
// string escapes. The regular JSON envelope also preserves nil versus [].
type storedRouterDraft struct {
	Name, TemplateID                string
	Groups                          []storedRouterGroup
	PersonalOrder                   []string
	DisabledRules, DisabledInbounds []string
}

type storedRouterGroup struct {
	Name       string
	Local      []storedRouterLocal
	Overrides  []storedRouterOverride
	Exclusions []configuration.ElementSource
}

type storedRouterLocal struct {
	ID      string
	Content []byte
}

type storedRouterOverride struct {
	Source  configuration.ElementSource
	Mode    configuration.ContentMode
	Values  map[string][]byte
	Content []byte
}

func encodeRouterDraft(draft routersettings.Draft) storedRouterDraft {
	stored := storedRouterDraft{
		Name: draft.Name, TemplateID: draft.TemplateID, PersonalOrder: draft.PersonalOrder,
		DisabledRules: draft.DisabledRules, DisabledInbounds: draft.DisabledInbounds,
	}
	if draft.Groups != nil {
		stored.Groups = make([]storedRouterGroup, len(draft.Groups))
	}
	for i, group := range draft.Groups {
		next := &stored.Groups[i]
		next.Name, next.Exclusions = group.Name, group.Exclusions
		if group.Local != nil {
			next.Local = make([]storedRouterLocal, len(group.Local))
		}
		for j, local := range group.Local {
			next.Local[j] = storedRouterLocal{ID: local.ID, Content: local.Content}
		}
		if group.Overrides != nil {
			next.Overrides = make([]storedRouterOverride, len(group.Overrides))
		}
		for j, override := range group.Overrides {
			next.Overrides[j] = storedRouterOverride{
				Source: override.Source, Mode: override.Mode, Content: override.Content,
				Values: convertRouterValues[[]byte](override.Values),
			}
		}
	}
	return stored
}

func (stored storedRouterDraft) draft(id string) routersettings.Draft {
	draft := routersettings.Draft{
		ID: id, Name: stored.Name, TemplateID: stored.TemplateID, PersonalOrder: stored.PersonalOrder,
		DisabledRules: stored.DisabledRules, DisabledInbounds: stored.DisabledInbounds,
	}
	if stored.Groups != nil {
		draft.Groups = make([]routersettings.Group, len(stored.Groups))
	}
	for i, group := range stored.Groups {
		next := &draft.Groups[i]
		next.Name, next.Exclusions = group.Name, group.Exclusions
		if group.Local != nil {
			next.Local = make([]routersettings.LocalElement, len(group.Local))
		}
		for j, local := range group.Local {
			next.Local[j] = routersettings.LocalElement{ID: local.ID, Content: local.Content}
		}
		if group.Overrides != nil {
			next.Overrides = make([]configuration.ContentOverride, len(group.Overrides))
		}
		for j, override := range group.Overrides {
			next.Overrides[j] = configuration.ContentOverride{
				Source: override.Source, Mode: override.Mode, Content: override.Content,
				Values: convertRouterValues[json.RawMessage](override.Values),
			}
		}
	}
	return draft
}

func convertRouterValues[To ~[]byte, From ~[]byte](values map[string]From) map[string]To {
	if values == nil {
		return nil
	}
	converted := make(map[string]To, len(values))
	for name, value := range values {
		converted[name] = To(value)
	}
	return converted
}
