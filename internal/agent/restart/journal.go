package restart

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"golang.org/x/sys/unix"
)

const journalName = "application.json"
const maxJournal = 4 * agentrevision.MaxBody

type storedRecord struct {
	Version       int
	Scope         preparation.Scope
	Current       *Confirmed
	LocalBaseline *preparation.Baseline `json:"LocalBaseline,omitempty"`
	LastRollback  *RollbackResult       `json:"LastRollback,omitempty"`
	Pending       *storedIntent
}

type storedIntent struct {
	Hot         *HotReceipt `json:",omitempty"`
	AttemptID   string
	Previous    preparation.Baseline
	Number      int64
	Tag         string
	TargetFiles []preparation.BaselineFile
	Phase       Phase
	Started     *preparation.Runtime
	Rollback    *RollbackProgress `json:"Rollback,omitempty"`
}

func storeRecord(record Record) storedRecord {
	stored := storedRecord{Version: record.Version, Scope: record.Scope, Current: record.Current, LocalBaseline: record.LocalBaseline, LastRollback: record.LastRollback}
	if p := record.Pending; p != nil {
		stored.Pending = &storedIntent{AttemptID: p.AttemptID, Previous: p.Previous, Number: p.Target.Snapshot.Number,
			Tag: p.Target.Tag, TargetFiles: p.TargetFiles, Phase: p.Phase, Started: p.Started, Rollback: p.Rollback, Hot: p.Hot}
	}
	return stored
}

func (stored storedRecord) restore() Record {
	record := Record{Version: stored.Version, Scope: stored.Scope, Current: stored.Current, LocalBaseline: stored.LocalBaseline, LastRollback: stored.LastRollback}
	if p := stored.Pending; p != nil {
		target := Desired{Tag: p.Tag, Snapshot: agentrevision.Snapshot{Number: p.Number, Profile: stored.Scope.Profile}}
		for _, file := range p.TargetFiles {
			target.Snapshot.Files = append(target.Snapshot.Files, configuration.CatalogFile{Name: file.Name, Content: bytes.Clone(file.Content)})
		}
		record.Pending = &Intent{AttemptID: p.AttemptID, Previous: p.Previous, Target: target, TargetFiles: p.TargetFiles, Phase: p.Phase, Started: p.Started, Rollback: p.Rollback, Hot: p.Hot}
	}
	return record
}

// DiskJournal holds the exclusive scope lock for application or recovery.
type DiskJournal struct {
	dir, lock *os.File
	scope     preparation.Scope
	syncDir   func() error
	syncFile  func(*os.File) error
}

func OpenJournal(path string, scope preparation.Scope) (*DiskJournal, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !validScope(scope) {
		return nil, ErrJournal
	}
	fd, err := unix.Open(path, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrJournal
	}
	dir := os.NewFile(uintptr(fd), "application journal directory")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0700 {
		dir.Close()
		return nil, ErrJournal
	}
	lock, err := journalFile(fd, "application.lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		dir.Close()
		return nil, ErrJournal
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		lock.Close()
		dir.Close()
		return nil, ErrPending
	}
	parent, parentErr := os.Open(filepath.Dir(path))
	if parentErr == nil {
		parentErr = errors.Join(parent.Sync(), parent.Close())
	}
	if parentErr != nil {
		lock.Close()
		dir.Close()
		return nil, ErrJournal
	}
	return &DiskJournal{dir: dir, lock: lock, scope: scope, syncDir: dir.Sync, syncFile: (*os.File).Sync}, nil
}

func (j *DiskJournal) Close() error {
	return errors.Join(j.lock.Close(), j.dir.Close())
}

func (j *DiskJournal) Load() (Record, error) {
	f, err := journalFile(int(j.dir.Fd()), journalName, unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return Record{Version: Version, Scope: j.scope}, j.syncDirectory()
	}
	if err != nil {
		return Record{}, ErrJournal
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxJournal+1))
	var stored storedRecord
	if err != nil || len(raw) > maxJournal || jsonv2.Unmarshal(raw, &stored, jsonv2.RejectUnknownMembers(true)) != nil {
		return Record{}, ErrJournal
	}
	result := stored.restore()
	if !validRecord(j.scope, result) || f.Sync() != nil || j.syncDirectory() != nil {
		return Record{}, ErrJournal
	}
	return result, nil
}

func (j *DiskJournal) Save(record Record) error {
	if !validRecord(j.scope, record) {
		return ErrJournal
	}
	raw, err := json.Marshal(storeRecord(record))
	if err != nil || len(raw) > maxJournal {
		return ErrJournal
	}
	fd := int(j.dir.Fd())
	var unique [16]byte
	rand.Read(unique[:])
	name := ".application-" + hex.EncodeToString(unique[:])
	f, err := journalFile(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return ErrJournal
	}
	defer unix.Unlinkat(fd, name, 0)
	n, writeErr := f.Write(raw)
	syncErr := j.syncFile(f)
	closeErr := f.Close()
	if n != len(raw) || writeErr != nil || syncErr != nil || closeErr != nil || unix.Renameat(fd, name, fd, journalName) != nil {
		return ErrJournal
	}
	return j.syncDirectory()
}

func (j *DiskJournal) syncDirectory() error {
	if j.syncDir() != nil {
		return ErrJournal
	}
	return nil
}

func journalFile(dir int, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(dir, name, flags|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 {
		unix.Close(fd)
		return nil, ErrJournal
	}
	return os.NewFile(uintptr(fd), "application journal"), nil
}

func validScope(scope preparation.Scope) bool {
	return scope.Origin != "" && scope.RouterID != "" && scope.Profile != "" && scope.VerifierID != ""
}

func validRecord(scope preparation.Scope, record Record) bool {
	if record.Version != Version || record.Scope != scope {
		return false
	}
	if current := record.Current; current != nil {
		if !current.Hot.valid() || current.Hot != nil && current.Hot.Guard != current.Guard || !validAttemptID(current.AttemptID) || !validBaseline(scope, current.Baseline) {
			return false
		}
		snapshot := agentrevision.Snapshot{Number: current.Number, Profile: scope.Profile}
		for _, file := range current.Baseline.Files {
			snapshot.Files = append(snapshot.Files, configuration.CatalogFile{Name: file.Name, Content: file.Content})
		}
		if !validTarget(scope, Desired{Tag: current.Tag, Snapshot: snapshot}) {
			return false
		}
	}
	if local := record.LocalBaseline; local != nil && (record.Current != nil || record.LastRollback == nil || !validBaseline(scope, *local)) {
		return false
	}
	if result := record.LastRollback; result != nil {
		if !validAttemptID(result.AttemptID) || !agentrevision.ValidTag(result.TargetTag) ||
			result.TargetNumber < 1 || !validOriginalPhase(result.FailedPhase) ||
			(result.RetryApproved && (record.Pending != nil || (record.Current != nil && record.Current.Number >= result.TargetNumber))) ||
			(record.Current != nil && record.Current.AttemptID == result.AttemptID) ||
			(record.Pending != nil && record.Pending.AttemptID == result.AttemptID) {
			return false
		}
	}
	pending := record.Pending
	working := record.Working()
	return pending == nil || validAttemptID(pending.AttemptID) && validTarget(scope, pending.Target) &&
		validBaseline(scope, pending.Previous) && validPending(scope, pending) &&
		(working == nil || reflect.DeepEqual(pending.Previous.Files, working.Baseline.Files)) &&
		(record.Current == nil || pending.AttemptID != record.Current.AttemptID && pending.Target.Snapshot.Number > record.Current.Number)
}

func validPending(scope preparation.Scope, pending *Intent) bool {
	baseline := preparation.Baseline{Runtime: pending.Previous.Runtime, Files: pending.TargetFiles}
	if !validBaseline(scope, baseline) || len(pending.TargetFiles) != len(pending.Target.Snapshot.Files) {
		return false
	}
	for i, file := range pending.TargetFiles {
		target := pending.Target.Snapshot.Files[i]
		if file.Name != target.Name || !bytes.Equal(file.Content, target.Content) {
			return false
		}
	}
	if state := pending.Rollback; state != nil &&
		((state.State != RollbackActive && state.State != RollbackFailed) ||
			(state.Started != nil && !validRollbackRuntime(scope, *pending, *state.Started))) {
		return false
	}
	if !pending.Hot.valid() {
		return false
	}
	if pending.Hot != nil {
		switch pending.Phase {
		case HotDispatch, HotVerified:
			return pending.Started == nil
		case HotFiles:
			return pending.Started != nil && *pending.Started == pending.Previous.Runtime
		}
		return false
	}
	switch pending.Phase {
	case Planned, Stopped, FilesVerified:
		return pending.Started == nil
	case RuntimeStarted:
		return pending.Started != nil && pending.Started.PID > 0 && pending.Started.StartTick > 0 &&
			pending.Started.VerifierID == scope.VerifierID && *pending.Started != pending.Previous.Runtime
	}
	return false
}

func validTarget(scope preparation.Scope, target Desired) bool {
	tag, err := agentrevision.Tag(scope.RouterID, target.Snapshot)
	return target.Snapshot.Profile == scope.Profile && err == nil && tag == target.Tag
}

func validBaseline(scope preparation.Scope, baseline preparation.Baseline) bool {
	_, err := preparation.BaselineIdentity(scope, baseline)
	return err == nil
}

func validAttemptID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == id
}
