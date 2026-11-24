package statefile

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/contact"
	"golang.org/x/sys/unix"
)

const reportName = "report.json"

type reportRecord struct {
	Version int           `json:"version"`
	Entry   contact.Entry `json:"entry"`
}

func (s *Store) LoadReport() (*contact.Entry, error) {
	raw, err := s.readPrivate(reportName, maxRecord)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, contact.ErrState
	}
	var record reportRecord
	if jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil || record.Version != 1 {
		return nil, contact.ErrState
	}
	return &record.Entry, nil
}
func (s *Store) SaveReport(entry contact.Entry) error {
	raw, err := json.Marshal(reportRecord{Version: 1, Entry: entry})
	if err != nil || len(raw) > maxRecord {
		return contact.ErrState
	}
	if s.writePrivate(reportName, raw) != nil {
		return contact.ErrState
	}
	return nil
}
