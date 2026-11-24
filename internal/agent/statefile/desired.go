package statefile

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

const DesiredName = "desired.json"

const maxDesiredRecord = agentrevision.MaxBody + maxRecord

type desiredRecord struct {
	Version int           `json:"version"`
	Entry   desired.Entry `json:"entry"`
}

func (s *Store) LoadDesired() (*desired.Entry, error) {
	raw, err := s.readPrivate(DesiredName, maxDesiredRecord)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, enrollment.ErrState
	}
	var record desiredRecord
	if jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil || record.Version != 1 {
		return nil, enrollment.ErrState
	}
	return &record.Entry, nil
}

func (s *Store) SaveDesired(entry desired.Entry) error {
	raw, err := json.Marshal(desiredRecord{Version: 1, Entry: entry})
	if err != nil || len(raw) > maxDesiredRecord {
		return enrollment.ErrState
	}
	return s.writePrivate(DesiredName, raw)
}
