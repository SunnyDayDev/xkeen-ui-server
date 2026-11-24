// Package desired reconciles the complete desired snapshot of an enrolled agent.
package desired

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

type Entry struct {
	Origin   string          `json:"origin"`
	RouterID string          `json:"routerId"`
	Tag      string          `json:"etag"`
	Body     json.RawMessage `json:"snapshot"`
}

type Cache interface {
	LoadDesired() (*Entry, error)
	SaveDesired(Entry) error
}

type ResultKind uint8

const (
	Complete ResultKind = iota + 1
	NotModified
	Absent
)

type Response struct {
	Kind ResultKind
	Tag  string
	Body []byte
}

type Remote interface {
	ReadDesired(ctx context.Context, key, tag string) (Response, error)
}

// Sync returns the verified current snapshot, or nil when the server has none.
// A failed read never returns the cached snapshot as current.
func Sync(ctx context.Context, state enrollment.State, cache Cache, remote Remote) (*agentrevision.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := agentcredential.Parse(state.Key, agentcredential.Working); !ok ||
		state.Origin == "" || state.RouterID == "" || state.InvitationDigest != "" {
		return nil, enrollment.ErrState
	}
	var tag string
	var local *agentrevision.Snapshot
	if entry, err := cache.LoadDesired(); err == nil && entry != nil &&
		entry.Origin == state.Origin && entry.RouterID == state.RouterID {
		if verified, err := agentrevision.Decode(entry.RouterID, entry.Tag, entry.Body); err == nil {
			tag, local = entry.Tag, verified
		}
	}
	reply, err := remote.ReadDesired(ctx, state.Key, tag)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reply.Kind == NotModified {
		if local != nil && reply.Tag == tag && len(reply.Body) == 0 {
			return local, nil
		}
		// Treat an unexplained 304 as a request to check without a precondition.
		reply, err = remote.ReadDesired(ctx, state.Key, "")
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch reply.Kind {
	case Absent:
		if reply.Tag == "" && len(reply.Body) == 0 {
			return nil, nil
		}
	case Complete:
		snapshot, err := agentrevision.Decode(state.RouterID, reply.Tag, reply.Body)
		if err != nil {
			return nil, enrollment.ErrProtocol
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if local != nil {
			if snapshot.Number < local.Number || (snapshot.Number == local.Number && reply.Tag != tag) {
				return nil, enrollment.ErrProtocol
			}
			if reply.Tag == tag {
				return snapshot, nil
			}
		}
		if err := cache.SaveDesired(Entry{Origin: state.Origin, RouterID: state.RouterID, Tag: reply.Tag, Body: reply.Body}); err != nil {
			return nil, enrollment.ErrState
		}
		return snapshot, nil
	}
	return nil, enrollment.ErrProtocol
}

// UntilCurrent is the initial reconciliation, with bounded delays between reads.
// Later reconciliation triggers can call Sync directly.
func UntilCurrent(ctx context.Context, state enrollment.State, cache Cache, remote Remote) (*agentrevision.Snapshot, error) {
	delay := 250 * time.Millisecond
	for {
		snapshot, err := Sync(ctx, state, cache, remote)
		if !errors.Is(err, enrollment.ErrTemporary) && !errors.Is(err, enrollment.ErrProtocol) {
			return snapshot, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay = min(2*delay, 5*time.Second)
	}
}
