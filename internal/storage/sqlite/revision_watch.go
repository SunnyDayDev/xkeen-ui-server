package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

// ReadWatch reads the active credential generation and latest revision in one
// SQLite statement. It does not load or decode the files.
func (p *Publications) ReadWatch(ctx context.Context, id agentcredentials.Identity) (int64, error) {
	var number int64
	err := p.db.QueryRowContext(ctx, `SELECT coalesce((SELECT max(revision)
 FROM desired_router_revisions WHERE router_id = a.router_id), 0)
 FROM agent_registrations a WHERE a.router_id = ? AND a.generation = ? AND a.active = 1`,
		id.RouterID, id.Generation).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, agentcredentials.ErrUnauthorized
	}
	return number, err
}
