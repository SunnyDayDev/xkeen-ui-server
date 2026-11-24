package sqlite

import (
	"context"
	"database/sql"
	jsonv2 "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

// Publications implements the publication store against the central SQLite file.
type Publications struct{ db *sql.DB }

func NewPublications(db *sql.DB) *Publications { return &Publications{db: db} }

var _ publication.Store = (*Publications)(nil)

const (
	readAcceptedTemplate = "SELECT draft, version FROM accepted_templates WHERE template_id = ? ORDER BY version DESC LIMIT 1"
	readTemplateDraft    = "SELECT draft, version FROM templates WHERE id = ?"
)

func (p *Publications) Read(ctx context.Context, request publication.ReadRequest) (*publication.Inputs, error) {
	if !validPublicationID(request.RouterID) ||
		(request.InitialTemplateID != "" && !validPublicationID(request.InitialTemplateID)) {
		return nil, publication.ErrInvalidState
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, operationError("read publication", ctx.Err())
	}
	defer tx.Rollback()

	inputs := &publication.Inputs{
		RouterID:               request.RouterID,
		TemplateDraftRequested: request.IncludeTemplateDraft,
		RouterDraftRequested:   request.IncludeRouterDraft,
	}
	inputs.Active, err = readActivePublication(ctx, tx, request.RouterID)
	if err != nil {
		return nil, err
	}
	templateID := request.InitialTemplateID
	if inputs.Active != nil {
		if templateID != "" && templateID != inputs.Active.Draft.TemplateID {
			return nil, publication.ErrInvalidState
		}
		templateID = inputs.Active.Draft.TemplateID
	}
	inputs.TemplateID = templateID
	if templateID != "" {
		inputs.Accepted, err = readPublicationTemplate(ctx, tx, readAcceptedTemplate, templateID)
		if err != nil {
			return nil, err
		}
		if request.IncludeTemplateDraft {
			inputs.TemplateDraft, err = readPublicationTemplate(ctx, tx, readTemplateDraft, templateID)
			if err != nil {
				return nil, err
			}
		}
	} else if request.IncludeTemplateDraft {
		return nil, publication.ErrInvalidState
	}
	if request.IncludeRouterDraft {
		inputs.RouterDraft, err = readPublicationRouterDraft(ctx, tx, request.RouterID)
		if err != nil {
			return nil, err
		}
	}
	inputs.Desired, err = readDesiredRevision(ctx, tx, request.RouterID)
	if err != nil {
		return nil, err
	}
	if (inputs.Active == nil) != (inputs.Desired == nil) ||
		(inputs.Active != nil && !validActiveSource(inputs.Active, inputs.Accepted)) {
		return nil, publication.ErrStoredState
	}
	if err := tx.Commit(); err != nil {
		return nil, operationError("finish reading publication", ctx.Err())
	}
	return inputs, nil
}

func validActiveSource(active *publication.ActiveSettings, accepted *templates.Snapshot) bool {
	if accepted == nil {
		return false
	}
	basis := active.OrderBasis
	return basis == nil || basis.TemplateVersion < accepted.Version || basisMatchesTemplate(basis, accepted)
}

func basisMatchesTemplate(basis *publication.OrderBasis, template *templates.Snapshot) bool {
	if template == nil || basis.TemplateVersion != template.Version || basis.TemplateID != template.ID {
		return false
	}
	var order []string
	for _, section := range template.Sections {
		if section.Name != "rules" {
			continue
		}
		for _, item := range section.Elements {
			order = append(order, item.Element.ID)
		}
	}
	return slices.Equal(order, basis.SharedRuleIDs)
}

func readPublicationTemplate(ctx context.Context, tx *sql.Tx, query, id string) (*templates.Snapshot, error) {
	var raw []byte
	var version int64
	err := tx.QueryRowContext(ctx, query, id).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, operationError("read publication template", ctx.Err())
	}
	draft, err := decodeTemplate(id, raw)
	if err != nil || version < 1 || len(configuration.ValidateTemplateDraft(*draft)) != 0 {
		return nil, publication.ErrStoredState
	}
	return &templates.Snapshot{TemplateDraft: *draft, Version: version}, nil
}

func readPublicationRouterDraft(ctx context.Context, tx *sql.Tx, id string) (*publication.VersionedRouterDraft, error) {
	var raw []byte
	var generation int64
	err := tx.QueryRowContext(ctx, "SELECT snapshot, generation FROM router_settings WHERE id = ?", id).Scan(&raw, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, operationError("read publication router draft", ctx.Err())
	}
	draft, err := decodeRouterDraft(id, raw)
	if err != nil || generation < 1 || routersettings.ValidateDraft(draft) != nil {
		return nil, publication.ErrStoredState
	}
	return &publication.VersionedRouterDraft{Draft: draft, Generation: generation}, nil
}

func readActivePublication(ctx context.Context, tx *sql.Tx, id string) (*publication.ActiveSettings, error) {
	var templateID string
	var raw, rawBasis []byte
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT template_id, settings, order_basis, generation
		FROM active_router_publications WHERE router_id = ?`, id).Scan(&templateID, &raw, &rawBasis, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, operationError("read active publication", ctx.Err())
	}
	draft, err := decodeRouterDraft(id, raw)
	if err != nil || generation < 1 || draft.TemplateID != templateID || routersettings.ValidateDraft(draft) != nil {
		return nil, publication.ErrStoredState
	}
	var basis *publication.OrderBasis
	if rawBasis != nil {
		basis = new(publication.OrderBasis)
		if err := jsonv2.Unmarshal(rawBasis, basis, jsonv2.RejectUnknownMembers(true)); err != nil {
			return nil, publication.ErrStoredState
		}
	}
	if !validStoredBasis(draft, basis) {
		return nil, publication.ErrStoredState
	}
	return &publication.ActiveSettings{Draft: draft, OrderBasis: basis, Generation: generation}, nil
}

func validStoredBasis(draft routersettings.Draft, basis *publication.OrderBasis) bool {
	if draft.PersonalOrder == nil {
		return basis == nil
	}
	if basis == nil || basis.TemplateID != draft.TemplateID || basis.TemplateVersion < 1 || basis.SharedRuleIDs == nil {
		return false
	}
	seen := make(map[string]struct{}, len(basis.SharedRuleIDs))
	for _, id := range basis.SharedRuleIDs {
		if _, exists := seen[id]; !configuration.ValidElementID(id) || exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

// ReadDesired returns the latest committed revision for one router.
func (p *Publications) ReadDesired(ctx context.Context, routerID string) (*publication.Revision, error) {
	if !validPublicationID(routerID) {
		return nil, publication.ErrInvalidState
	}
	return readDesiredRevision(ctx, p.db, routerID)
}

func readDesiredRevision(ctx context.Context, source interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (*publication.Revision, error) {
	var profile string
	var number int64
	var raw []byte
	err := source.QueryRowContext(ctx, `SELECT revision, profile, files FROM desired_router_revisions
		WHERE router_id = ? ORDER BY revision DESC LIMIT 1`, id).Scan(&number, &profile, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, operationError("read desired revision", ctx.Err())
	}
	var records []publicationFileRecord
	if err := jsonv2.Unmarshal(raw, &records, jsonv2.RejectUnknownMembers(true)); err != nil ||
		number < 1 || !validPublicationID(profile) || !validFileRecords(records) {
		return nil, publication.ErrStoredState
	}
	files := make([]configuration.CatalogFile, len(records))
	for i, record := range records {
		files[i] = configuration.CatalogFile{Name: record.Name, Content: record.Bytes}
	}
	return &publication.Revision{Number: number, Catalog: publication.VerifiedFiles{Profile: profile, Files: files}}, nil
}

type publicationFileRecord struct {
	Name  string
	Bytes []byte
}

func validFileRecords(records []publicationFileRecord) bool {
	if len(records) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, exists := seen[record.Name]; !validPublicationFilename(record.Name) || exists || len(record.Bytes) == 0 {
			return false
		}
		seen[record.Name] = struct{}{}
	}
	return true
}

func validPublicationID(id string) bool { return id != "" && utf8.ValidString(id) }

func validPublicationFilename(name string) bool {
	return validPublicationID(name) && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}
