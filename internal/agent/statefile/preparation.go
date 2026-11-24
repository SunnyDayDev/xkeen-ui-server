package statefile

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"golang.org/x/sys/unix"
)

const preparedName = "prepared.json"
const maxPreparedRecord = 2*agentrevision.MaxBody + maxRecord

// Content intentionally encodes as base64. RawMessage serialization would
// compact or re-escape the catalog and destroy exact byte identity.
type preparedFile struct {
	Name    string `json:"name"`
	Content []byte `json:"contentBase64"`
}

type preparedRecord struct {
	Version    int                  `json:"version"`
	Scope      preparation.Scope    `json:"scope"`
	BaselineID string               `json:"baselineId"`
	Baseline   preparation.Baseline `json:"baseline"`
	Number     int64                `json:"revision"`
	Tag        string               `json:"tag"`
	Files      []preparedFile       `json:"files"`
}

func (s *Store) SavePrepared(p preparation.Package) error {
	if !p.Valid() {
		return preparation.ErrState
	}
	record := preparedRecord{Version: 1, Scope: p.Scope, BaselineID: p.BaselineID,
		Baseline: p.Baseline, Number: p.Candidate.Number, Tag: p.CandidateTag}
	for _, file := range p.Candidate.Files {
		record.Files = append(record.Files, preparedFile{Name: file.Name, Content: file.Content})
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > maxPreparedRecord || s.writePrivate(preparedName, raw) != nil {
		return preparation.ErrState
	}
	return nil
}

func (s *Store) LoadPrepared() (*preparation.Package, error) {
	raw, err := s.readPrivate(preparedName, maxPreparedRecord)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	var record preparedRecord
	if err != nil || jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil || record.Version != 1 {
		return nil, preparation.ErrState
	}
	candidate := agentrevision.Snapshot{Number: record.Number, Profile: record.Scope.Profile}
	for _, file := range record.Files {
		candidate.Files = append(candidate.Files, configuration.CatalogFile{Name: file.Name, Content: file.Content})
	}
	p := &preparation.Package{Scope: record.Scope, BaselineID: record.BaselineID, Baseline: record.Baseline,
		Candidate: candidate, CandidateTag: record.Tag}
	if !p.Valid() {
		return nil, preparation.ErrState
	}
	return p, nil
}

func (s *Store) BaselineID() (string, error) {
	p, err := s.LoadPrepared()
	if p == nil {
		return "", err
	}
	return p.BaselineID, nil
}
