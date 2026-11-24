package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

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
	result, err := r.db.ExecContext(ctx, `INSERT INTO router_settings (id, snapshot) VALUES (?, ?)
		ON CONFLICT (id) DO UPDATE SET snapshot = excluded.snapshot, generation = generation + 1, order_basis = NULL
		WHERE generation < ?`, draft.ID, snapshot, int64(math.MaxInt64))
	if err != nil {
		return operationError("write router draft", ctx.Err())
	}
	n, err := result.RowsAffected()
	if err != nil {
		return operationError("write router draft", ctx.Err())
	}
	if n == 0 {
		return errors.New("router draft version exhausted")
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
		return routersettings.Draft{}, false, operationError("read router draft", ctx.Err())
	}
	draft, err := decodeRouterDraft(id, raw)
	return draft, err == nil, err
}

func decodeRouterDraft(id string, raw []byte) (routersettings.Draft, error) {
	tree, issue := jsonvalue.Parse(raw)
	if issue != nil || !validRouterRecord(tree) {
		return routersettings.Draft{}, routersettings.ErrStoredDraft
	}
	var stored storedRouterDraft
	if err := json.Unmarshal(raw, &stored); err != nil {
		return routersettings.Draft{}, routersettings.ErrStoredDraft
	}
	return stored.draft(id), nil
}

// Check the exact stored field names before decoding. encoding/json accepts
// case-insensitive aliases and treats a missing slice as nil.
func validRouterRecord(tree any) bool {
	fields := []string{"Name", "TemplateID", "Groups", "PersonalOrder", "DisabledRules", "DisabledInbounds"}
	if object, ok := tree.(map[string]any); ok {
		if raw, exists := object["Exclusion"]; exists {
			part := routerRecord(raw, "Source", "Values", "Replacement")
			if part == nil || !validRouterSource(part["Source"]) {
				return false
			}
			if raw := part["Values"]; raw != nil {
				values, ok := raw.(map[string]any)
				if !ok {
					return false
				}
				for _, v := range values {
					if _, ok := v.(string); !ok {
						return false
					}
				}
			}
			fields = append(fields, "Exclusion")
		}
	}
	record := routerRecord(tree, fields...)
	if record == nil {
		return false
	}
	if _, ok := record["Name"].(string); !ok {
		return false
	}
	return routerRecords(record["Groups"], func(value any) bool {
		group := routerRecord(value, "Name", "Local", "Overrides", "Exclusions")
		return group != nil &&
			routerRecords(group["Local"], func(item any) bool {
				return routerRecord(item, "ID", "Content") != nil
			}) &&
			routerRecords(group["Overrides"], func(item any) bool {
				override := routerRecord(item, "Source", "Mode", "Values", "Content")
				return override != nil && validRouterSource(override["Source"])
			}) &&
			routerRecords(group["Exclusions"], validRouterSource)
	})
}

func validRouterSource(value any) bool {
	return routerRecord(value, "TemplateID", "ElementID") != nil
}

func routerRecord(value any, fields ...string) map[string]any {
	record, ok := value.(map[string]any)
	if !ok || len(record) != len(fields) {
		return nil
	}
	for _, field := range fields {
		if _, ok := record[field]; !ok {
			return nil
		}
	}
	return record
}

func routerRecords(value any, valid func(any) bool) bool {
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !valid(item) {
			return false
		}
	}
	return true
}

// Store raw JSON as bytes. Embedding RawMessage would compact it and may rewrite
// string escapes. The regular JSON envelope also preserves nil versus [].
type storedRouterDraft struct {
	Exclusion                       *routersettings.ExclusionOverride `json:",omitempty"`
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
		Exclusion: draft.Exclusion, Name: draft.Name, TemplateID: draft.TemplateID, PersonalOrder: draft.PersonalOrder,
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
		Exclusion: stored.Exclusion, ID: id, Name: stored.Name, TemplateID: stored.TemplateID, PersonalOrder: stored.PersonalOrder,
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
