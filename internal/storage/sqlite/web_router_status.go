package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type WebRouterStatus struct{ db *sql.DB }

func NewWebRouterStatus(db *sql.DB) WebRouterStatus {
	return WebRouterStatus{db: db}
}

func (s WebRouterStatus) List(ctx context.Context, digest [32]byte, clock func() time.Time) ([]webadmin.RouterStatus, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := clock()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, now)); err != nil {
		return nil, err
	}
	return readRouterStatuses(ctx, tx, now, false)
}

func (s *ExternalAccess) ReadExternalRouterStatuses(ctx context.Context, permit externalaccess.Permit) ([]reporting.RouterStatus, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return nil, err
	}
	return readRouterStatuses(ctx, tx, s.now(), true)
}

func readRouterStatuses(ctx context.Context, tx *sql.Tx, now time.Time, includeRecovery bool) ([]reporting.RouterStatus, error) {
	rows, err := tx.QueryContext(ctx, `SELECT active.router_id, active.template_id,
		json_extract(CAST(active.settings AS TEXT), '$.Name', '$.TemplateID'),
		(SELECT name FROM router_display_names WHERE router_id=active.router_id),
		contact.contact_ns, contact.applied, contact.uncertain, CASE WHEN ? THEN contact.report END,
		coalesce((SELECT max(revision) FROM desired_router_revisions WHERE router_id=active.router_id), 0)`+
		visibleRouterSource+` ORDER BY active.router_id COLLATE BINARY`, includeRecovery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]reporting.RouterStatus, 0)
	for rows.Next() {
		var row reporting.RouterStatus
		var metadata, report []byte
		var displayed sql.NullString
		contact := contactRecord{observed: true}
		if err := rows.Scan(&row.RouterID, &row.TemplateID, &metadata, &displayed, &contact.ns, &contact.applied, &contact.uncertain, &report, &row.Desired); err != nil {
			return nil, err
		}
		var names [2]*string
		if !validPublicationID(row.RouterID) || !validPublicationID(row.TemplateID) || row.Desired < 1 ||
			contact.applied < 0 || contact.uncertain < 0 || !utf8.Valid(metadata) ||
			json.Unmarshal(metadata, &names) != nil || names[1] == nil || *names[1] != row.TemplateID {
			return nil, publication.ErrStoredState
		}
		row.Name = row.RouterID
		if names[0] != nil && *names[0] != "" {
			row.Name = *names[0]
		}
		if displayed.Valid {
			if !routersettings.ValidDisplayName(displayed.String) {
				return nil, publication.ErrStoredState
			}
			row.Name = displayed.String
		}
		status := projectContactStatus(contact, row.Desired, now)
		row.Contact, row.Applied, row.Runtime = status.Presence, status.Applied, status.Runtime
		if report != nil {
			last, err := reporting.DecodeReport(report)
			if err != nil || last == nil {
				return nil, publication.ErrStoredState
			}
			if last.State == "failed" && last.Code == "rollback_failed" && row.Runtime == "unknown" {
				row.Recovery = "rollback_failed"
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
