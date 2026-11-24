package desired

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type fakeCache struct {
	entry *Entry
	err   error
	saves int
}

func (f *fakeCache) LoadDesired() (*Entry, error) { return f.entry, f.err }
func (f *fakeCache) SaveDesired(entry Entry) error {
	f.saves++
	f.entry = &entry
	return nil
}

type fakeRemote func(context.Context, string, string) (Response, error)

func (f fakeRemote) ReadDesired(ctx context.Context, key, tag string) (Response, error) {
	return f(ctx, key, tag)
}

func desiredFixture(t *testing.T, number int64) (enrollment.State, Entry) {
	t.Helper()
	scope := enrollment.State{
		Origin: "https://server.example", RouterID: "device.example", Key: agentcredential.Generate(agentcredential.Working).Encode(),
	}
	snapshot := agentrevision.Snapshot{Number: number, Profile: configuration.XrayIdentityVersion}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		snapshot.Files = append(snapshot.Files, configuration.CatalogFile{Name: name, Content: []byte(" {}\n")})
	}
	tag, body, err := agentrevision.Encode(scope.RouterID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return scope, Entry{Origin: scope.Origin, RouterID: scope.RouterID, Tag: tag, Body: body}
}

func TestDesiredSyncOnlyConditionsOnCompleteScopedCache(t *testing.T) {
	scope, previous := desiredFixture(t, 3)
	for name, modify := range map[string]func(*Entry){
		"valid":        nil,
		"wrong origin": func(e *Entry) { e.Origin = "https://other.example" },
		"wrong router": func(e *Entry) { e.RouterID = "other.example" },
		"damaged":      func(e *Entry) { e.Body = e.Body[:len(e.Body)-1] },
	} {
		t.Run(name, func(t *testing.T) {
			entry := previous
			if modify != nil {
				modify(&entry)
			}
			cache := &fakeCache{entry: &entry}
			got, err := Sync(t.Context(), scope, cache, fakeRemote(func(_ context.Context, key, tag string) (Response, error) {
				if key != scope.Key {
					t.Fatal("wrong bound credential")
				}
				if modify == nil {
					if tag != entry.Tag {
						t.Fatal("missing eligible cache condition")
					}
					return Response{Kind: NotModified, Tag: tag}, nil
				}
				if tag != "" {
					t.Fatal("invalid cache leaked into condition")
				}
				return Response{Kind: Absent}, nil
			}))
			if err != nil || cache.saves != 0 || (modify == nil && (got == nil || got.Number != 3)) || (modify != nil && got != nil) {
				t.Fatalf("sync = %v, %v, saves=%d", got, err, cache.saves)
			}
		})
	}
}

func TestDesiredSyncRetriesUnexpected304WithoutCondition(t *testing.T) {
	scope, previous := desiredFixture(t, 3)
	_, latest := desiredFixture(t, 8)
	cache := &fakeCache{entry: &previous}
	var tags []string
	got, err := Sync(t.Context(), scope, cache, fakeRemote(func(_ context.Context, _, tag string) (Response, error) {
		tags = append(tags, tag)
		if len(tags) == 1 {
			return Response{Kind: NotModified, Tag: latest.Tag}, nil
		}
		return Response{Kind: Complete, Tag: latest.Tag, Body: latest.Body}, nil
	}))
	if err != nil || got == nil || got.Number != 8 || cache.saves != 1 || !bytes.Equal(cache.entry.Body, latest.Body) ||
		!slices.Equal(tags, []string{previous.Tag, ""}) {
		t.Fatalf("sync = %v, %v, calls=%d, saves=%d", got, err, len(tags), cache.saves)
	}
}

func TestDesiredRetryPreservesTheOldCopyUntilACompleteFetch(t *testing.T) {
	scope, previous := desiredFixture(t, 3)
	_, latest := desiredFixture(t, 8)
	cache := &fakeCache{entry: &previous}
	calls := 0
	got, err := UntilCurrent(t.Context(), scope, cache, fakeRemote(func(_ context.Context, _, tag string) (Response, error) {
		calls++
		if tag != previous.Tag || cache.saves != 0 {
			t.Fatal("failed transfer advanced local state")
		}
		switch calls {
		case 1:
			return Response{}, enrollment.ErrTemporary
		case 2:
			return Response{Kind: Complete, Tag: latest.Tag, Body: latest.Body[:len(latest.Body)-1]}, nil
		default:
			return Response{Kind: Complete, Tag: latest.Tag, Body: latest.Body}, nil
		}
	}))
	if err != nil || got == nil || got.Number != 8 || calls != 3 || cache.saves != 1 {
		t.Fatalf("retry = %v, %v, calls=%d, saves=%d", got, err, calls, cache.saves)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err = UntilCurrent(ctx, scope, cache, fakeRemote(func(_ context.Context, _, _ string) (Response, error) {
		cancel()
		return Response{}, enrollment.ErrTemporary
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestDesiredSyncRejectsRegressedAndChangedImmutableRevisions(t *testing.T) {
	scope, previous := desiredFixture(t, 3)
	_, old := desiredFixture(t, 2)
	_, newest := desiredFixture(t, 4)
	changed, err := agentrevision.Decode(scope.RouterID, previous.Tag, previous.Body)
	if err != nil {
		t.Fatal(err)
	}
	changed.Files[0].Content = []byte("{ }")
	tag, body, err := agentrevision.Encode(scope.RouterID, *changed)
	if err != nil {
		t.Fatal(err)
	}
	for name, late := range map[string]Entry{"older": old, "divergent": {Tag: tag, Body: body}} {
		t.Run(name, func(t *testing.T) {
			cache := &fakeCache{entry: &previous}
			remote := fakeRemote(func(_ context.Context, _, condition string) (Response, error) {
				if condition != previous.Tag {
					t.Fatal("late transfer advanced the condition")
				}
				return Response{Kind: Complete, Tag: late.Tag, Body: late.Body}, nil
			})
			if got, err := Sync(t.Context(), scope, cache, remote); got != nil || !errors.Is(err, enrollment.ErrProtocol) || cache.saves != 0 {
				t.Fatalf("late immutable candidate was accepted, saves=%d, error=%v", cache.saves, err)
			}
			late = newest
			if got, err := Sync(t.Context(), scope, cache, remote); err != nil || got == nil || got.Number != 4 || cache.saves != 1 {
				t.Fatalf("newest candidate did not recover, saves=%d, error=%v", cache.saves, err)
			}
		})
	}
}

func TestDesiredSyncDoesNotWriteCanceledFetch(t *testing.T) {
	scope, previous := desiredFixture(t, 3)
	_, next := desiredFixture(t, 4)
	cache := &fakeCache{entry: &previous}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	got, err := Sync(ctx, scope, cache, fakeRemote(func(context.Context, string, string) (Response, error) {
		cancel()
		return Response{Kind: Complete, Tag: next.Tag, Body: next.Body}, nil
	}))
	if got != nil || !errors.Is(err, context.Canceled) || cache.saves != 0 {
		t.Fatalf("canceled fetch changed state, saves=%d, error=%v", cache.saves, err)
	}
}
