package sqlite

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestExternalSettingsSerialiseAccessAndSession(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	admin := NewWebAdmin(db)
	if err := admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{1}}, false); err != nil {
		t.Fatal(err)
	}
	session := [32]byte{1}
	if err := admin.Issue(ctx, 1, session, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	s := NewExternalAccess(db, func() time.Time { return now })
	get := func() externalaccess.Settings {
		t.Helper()
		v, secret, err := s.Owner(ctx, session, nil)
		if err != nil || secret != "" {
			t.Fatal(err)
		}
		return v
	}
	v := get()
	if v.Enabled || !v.Protected || v.HasToken || v.Version != 1 {
		t.Fatal(v)
	}
	v, _, err = s.Owner(ctx, session, &externalaccess.Change{Version: v.Version, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, nil); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal(err)
	}
	v, token, err := s.Owner(ctx, session, &externalaccess.Change{Version: v.Version, Rotate: true})
	if err != nil || !v.HasToken {
		t.Fatal(err)
	}
	p, err := s.Admit(ctx, []string{"Bearer " + token})
	if err != nil || p.Version != v.Version {
		t.Fatal(err)
	}
	var digest []byte
	if err = db.QueryRow("SELECT digest FROM external_access").Scan(&digest); err != nil || bytes.Contains(digest, []byte(token)) || len(digest) != 32 {
		t.Fatal("unexpected persisted verifier")
	}
	_, _, err = s.Owner(ctx, session, &externalaccess.Change{Version: 1, Rotate: true})
	if !errors.Is(err, externalaccess.ErrConflict) {
		t.Fatal(err)
	}
	v, next, err := s.Owner(ctx, session, &externalaccess.Change{Version: v.Version, Rotate: true})
	if err != nil || next == token {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, []string{"Bearer " + token}); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal(err)
	}
	v, _, err = s.Owner(ctx, session, &externalaccess.Change{Version: v.Version, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if get() != v {
		t.Fatal("wrong settings")
	}
	now = now.Add(webadmin.IdleLifetime)
	if _, _, err = s.Owner(ctx, session, &externalaccess.Change{Version: v.Version, Rotate: true}); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatal(err)
	}
}
