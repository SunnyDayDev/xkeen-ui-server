// Package reporting defines the router contact and application report contract.
package reporting

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"strconv"
	"time"
)

var (
	ErrInvalid         = errors.New("invalid report")
	ErrConflict        = errors.New("report conflict")
	ErrUnknownRevision = errors.New("unknown revision")
	ErrUnavailable     = errors.New("contact unavailable")
)

const Interval = 60 * time.Second
const OfflineAfter = 180 * time.Second

type Report struct {
	Sequence        int64  `json:"sequence,string"`
	Revision        int64  `json:"revision,string"`
	State           string `json:"state"`
	AppliedRevision int64  `json:"appliedRevision,string,omitempty"`
	Stage           string `json:"stage,omitempty"`
	Code            string `json:"code,omitempty"`
}

func (r Report) Valid() bool {
	if r.Sequence <= 0 || r.Revision <= 0 || r.AppliedRevision < 0 {
		return false
	}
	switch r.State {
	case "applying":
		return r.Stage == "" && r.Code == ""
	case "applied":
		return (r.AppliedRevision == 0 || r.AppliedRevision == r.Revision) && r.Stage == "" && r.Code == ""
	case "failed":
		switch r.Stage + "/" + r.Code {
		case "validate/configuration_invalid", "write/write_failed", "reload/reload_failed", "verify/runtime_check_failed", "verify/runtime_unknown", "rollback/rollback_failed", "apply/apply_failed":
			return true
		}
	}
	return false
}

type Presence string

const (
	Unobserved Presence = "unobserved"
	Online     Presence = "online"
	Offline    Presence = "offline"
)

type Status struct {
	Presence         Presence
	LastContact      time.Time
	Desired, Applied int64
	Last             *Report
	Runtime          string
}

// RouterStatus is the control-plane summary shared by the two management views.
type RouterStatus struct {
	RouterID, Name, TemplateID string
	Contact                    Presence
	Desired, Applied           int64
	Runtime, Recovery          string
}

const MaxBody = 1024

type Cursor struct {
	Sequence int64 `json:"sequence,string"`
}

func DecodeReport(raw []byte) (*Report, error) {
	var wire struct {
		Sequence        string          `json:"sequence"`
		Revision        string          `json:"revision"`
		AppliedRevision json.RawMessage `json:"appliedRevision"`
		State           string          `json:"state"`
		Stage           string          `json:"stage"`
		Code            string          `json:"code"`
	}
	if jsonv2.Unmarshal(raw, &wire, jsonv2.RejectUnknownMembers(true)) != nil {
		return nil, ErrInvalid
	}
	sequence, ok := positive(wire.Sequence)
	if !ok {
		return nil, ErrInvalid
	}
	revision, ok := positive(wire.Revision)
	if !ok {
		return nil, ErrInvalid
	}
	r := Report{Sequence: sequence, Revision: revision, State: wire.State, Stage: wire.Stage, Code: wire.Code}
	if wire.AppliedRevision != nil {
		var text string
		if json.Unmarshal(wire.AppliedRevision, &text) != nil {
			return nil, ErrInvalid
		}
		r.AppliedRevision, ok = positive(text)
		if !ok {
			return nil, ErrInvalid
		}
	}
	if !r.Valid() {
		return nil, ErrInvalid
	}
	return &r, nil
}

func positive(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

// Advances reports whether an incoming report changes the durable cursor.
func Advances(last *Report, incoming Report) (bool, error) {
	if last == nil || incoming.Sequence > last.Sequence {
		return true, nil
	}
	if incoming.Sequence == last.Sequence && incoming != *last {
		return false, ErrConflict
	}
	return false, nil
}

// Evidence separates the last confirmed revision from rollback uncertainty.
type Evidence struct{ Applied, Uncertain int64 }

func (e Evidence) After(r Report) Evidence {
	if r.State == "applied" {
		e.Applied = max(e.Applied, r.Revision)
		if r.Revision >= max(e.Uncertain, e.Applied) {
			e.Uncertain = 0
		}
	}
	if r.Code == "rollback_failed" || r.Code == "runtime_unknown" {
		e.Uncertain = max(e.Uncertain, r.Revision)
	}
	return e
}
