package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration/elementjson"
)

// TemplateRepository stores one authored template per row.
type TemplateRepository struct{ db *sql.DB }

func NewTemplateRepository(db *sql.DB) TemplateRepository { return TemplateRepository{db: db} }

type templateSectionRecord struct {
	Name     string                  `json:"name"`
	Elements []templateElementRecord `json:"elements"`
}

type templateElementRecord struct {
	Disabled *bool           `json:"disabled"`
	Record   json.RawMessage `json:"record"`
}

func (r TemplateRepository) Insert(ctx context.Context, draft configuration.TemplateDraft) error {
	sections := make([]templateSectionRecord, len(draft.Sections))
	for i, section := range draft.Sections {
		sections[i] = templateSectionRecord{Name: section.Name, Elements: make([]templateElementRecord, len(section.Elements))}
		for j, item := range section.Elements {
			record, issues := elementjson.Encode(item.Element, draft.ElementLocation(i, j), nil)
			if len(issues) != 0 {
				return &templates.InvalidDraftError{Issues: issues}
			}
			sections[i].Elements[j] = templateElementRecord{Disabled: &item.Disabled, Record: record}
		}
	}
	raw, err := json.Marshal(sections)
	if err != nil {
		return templates.ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, "INSERT INTO templates (id, draft) VALUES (?, ?) ON CONFLICT(id) DO NOTHING", draft.ID, raw)
	if err != nil {
		return operationError("create template", ctx.Err())
	}
	n, err := result.RowsAffected()
	if err != nil {
		return errors.New("read template insert result")
	}
	if n == 0 {
		return templates.ErrConflict
	}
	return nil
}

func (r TemplateRepository) Find(ctx context.Context, id string) (*configuration.TemplateDraft, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, "SELECT draft FROM templates WHERE id = ?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, templates.ErrNotFound
	}
	if err != nil {
		return nil, operationError("read template", ctx.Err())
	}
	return decodeTemplate(id, raw)
}

func decodeTemplate(id string, raw []byte) (*configuration.TemplateDraft, error) {
	// The v2 decoder also rejects duplicates, invalid UTF-8 and non-exact field names.
	var sections []templateSectionRecord
	if err := jsonv2.Unmarshal(raw, &sections, jsonv2.RejectUnknownMembers(true)); err != nil || sections == nil {
		return nil, templates.ErrCorrupt
	}
	draft := &configuration.TemplateDraft{ID: id, Sections: make([]configuration.TemplateSection, len(sections))}
	for i, section := range sections {
		if section.Elements == nil {
			return nil, templates.ErrCorrupt
		}
		draft.Sections[i] = configuration.TemplateSection{Name: section.Name, Elements: make([]configuration.TemplateElement, len(section.Elements))}
		for j, item := range section.Elements {
			if item.Disabled == nil {
				return nil, templates.ErrCorrupt
			}
			element, issues := elementjson.Decode(item.Record, draft.ElementLocation(i, j))
			if len(issues) != 0 {
				return nil, fmt.Errorf("%w: %w", templates.ErrCorrupt, &templates.InvalidDraftError{Issues: issues})
			}
			draft.Sections[i].Elements[j] = configuration.TemplateElement{Element: *element, Disabled: *item.Disabled}
		}
	}
	return draft, nil
}

func operationError(operation string, cancellation error) error {
	if cancellation != nil {
		return fmt.Errorf("%s: %w", operation, cancellation)
	}
	return errors.New(operation + ": SQLite operation failed")
}
