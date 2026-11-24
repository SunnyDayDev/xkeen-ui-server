// Package agentrevision encodes complete desired snapshots for the agent channel.
package agentrevision

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	MaxCatalog = 8 << 20
	MaxBody    = 12 << 20
)

var ErrInvalid = errors.New("invalid desired snapshot")

type Snapshot struct {
	Number  int64
	Profile string
	Files   []configuration.CatalogFile
}

type wireSnapshot struct {
	Revision string     `json:"revision"`
	Profile  string     `json:"profile"`
	Files    []wireFile `json:"files"`
}

type wireFile struct {
	Name          string `json:"name"`
	ContentBase64 string `json:"contentBase64"`
}

var catalogNames = [...]string{
	"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json", "06_extra.json",
}

// Tag validates the exact named files and identifies their canonical representation.
func Tag(routerID string, snapshot Snapshot) (string, error) {
	return snapshotTag(routerID, snapshot.Number, snapshot.Profile, sortedFiles(snapshot.Files))
}

// Encode produces the canonical uncompressed representation and its strong tag.
func Encode(routerID string, snapshot Snapshot) (string, []byte, error) {
	files := sortedFiles(snapshot.Files)
	tag, err := snapshotTag(routerID, snapshot.Number, snapshot.Profile, files)
	if err != nil {
		return "", nil, err
	}
	wire := wireSnapshot{Revision: strconv.FormatInt(snapshot.Number, 10), Profile: snapshot.Profile}
	for _, file := range files {
		wire.Files = append(wire.Files, wireFile{file.Name, base64.StdEncoding.EncodeToString(file.Content)})
	}
	body, err := json.Marshal(wire)
	if err != nil || len(body) > MaxBody {
		return "", nil, ErrInvalid
	}
	return tag, body, nil
}

// Decode checks the shape, exact named bytes and scope before returning any input.
func Decode(routerID, tag string, body []byte) (*Snapshot, error) {
	var wire wireSnapshot
	if len(body) > MaxBody || !ValidTag(tag) ||
		jsonv2.Unmarshal(body, &wire, jsonv2.RejectUnknownMembers(true)) != nil ||
		len(wire.Files) < 5 || len(wire.Files) > len(catalogNames) {
		return nil, ErrInvalid
	}
	number, err := strconv.ParseInt(wire.Revision, 10, 64)
	if err != nil || number <= 0 || strconv.FormatInt(number, 10) != wire.Revision {
		return nil, ErrInvalid
	}
	files := make([]configuration.CatalogFile, 0, len(wire.Files))
	for _, file := range wire.Files {
		if strings.ContainsAny(file.ContentBase64, "\r\n") || len(file.ContentBase64) > base64.StdEncoding.EncodedLen(MaxCatalog) {
			return nil, ErrInvalid
		}
		content, err := base64.StdEncoding.Strict().DecodeString(file.ContentBase64)
		if err != nil {
			return nil, ErrInvalid
		}
		files = append(files, configuration.CatalogFile{Name: file.Name, Content: content})
	}
	files = sortedFiles(files)
	if want, err := snapshotTag(routerID, number, wire.Profile, files); err != nil || want != tag {
		return nil, ErrInvalid
	}
	return &Snapshot{Number: number, Profile: wire.Profile, Files: files}, nil
}

func ValidTag(tag string) bool {
	if len(tag) != 69 || !strings.HasPrefix(tag, `"v1-`) || tag[68] != '"' {
		return false
	}
	for i := 4; i < 68; i++ {
		b := tag[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func sortedFiles(files []configuration.CatalogFile) []configuration.CatalogFile {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b configuration.CatalogFile) int { return strings.Compare(a.Name, b.Name) })
	return sorted
}

func snapshotTag(routerID string, number int64, profile string, files []configuration.CatalogFile) (string, error) {
	if routerID == "" || number < 1 || profile != configuration.XrayIdentityVersion ||
		len(files) < 5 || len(files) > len(catalogNames) {
		return "", ErrInvalid
	}
	h := sha256.New()
	field := func(raw []byte) {
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(raw))))
		_, _ = h.Write(raw)
	}
	field([]byte("xkeen-desired-revision-v1"))
	field([]byte(routerID))
	field([]byte(strconv.FormatInt(number, 10)))
	field([]byte(profile))
	_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(files))))
	remaining := MaxCatalog
	for i, file := range files {
		if file.Name != catalogNames[i] || len(file.Content) == 0 || len(file.Content) > remaining {
			return "", ErrInvalid
		}
		remaining -= len(file.Content)
		field([]byte(file.Name))
		field(file.Content)
	}
	return `"v1-` + hex.EncodeToString(h.Sum(nil)) + `"`, nil
}
