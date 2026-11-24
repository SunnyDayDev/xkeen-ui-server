// Package validation checks immutable catalogs with an explicitly trusted worker.
package validation

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sort"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

const (
	maxCatalog = 8 << 20
	maxBody    = 12 << 20
	endpoint   = "/v1/validate"
)

var names = []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json", "06_extra.json"}

type file struct {
	Name  string
	Bytes []byte
}
type binding struct{ Attempt, Profile, Digest string }
type request struct {
	binding
	Files []file
}
type response struct {
	binding
	Outcome string
}

func newRequest(profile string, files []configuration.CatalogFile) (request, error) {
	var req request
	if len(files) < 5 || len(files) > 6 || profile != configuration.XrayIdentityVersion {
		return req, publication.ErrCatalogRejected
	}
	total := 0
	for _, f := range files {
		if len(f.Content) > maxCatalog-total {
			return request{}, publication.ErrValidatorUnavailable
		}
		if !slices.Contains(names, f.Name) {
			return request{}, publication.ErrCatalogRejected
		}
		total += len(f.Content)
		req.Files = append(req.Files, file{f.Name, bytes.Clone(f.Content)})
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return request{}, publication.ErrValidatorUnavailable
	}
	req.Attempt = hex.EncodeToString(nonce)
	req.Profile = profile
	digest, ok := req.manifest()
	if !ok {
		return request{}, publication.ErrCatalogRejected
	}
	req.Digest = digest
	return req, nil
}

// Length-prefix each field so boundaries and filename order are unambiguous.
func (r request) manifest() (string, bool) {
	if r.Profile != configuration.XrayIdentityVersion || len(r.Files) < 5 || len(r.Files) > 6 {
		return "", false
	}
	h := sha256.New()
	write := func(b []byte) { _, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b)))); _, _ = h.Write(b) }
	write([]byte("xkeen-validation-v1"))
	write([]byte(r.Profile))
	_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(r.Files))))
	sorted := slices.Clone(r.Files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	total := 0
	for i, f := range sorted {
		if f.Name != names[i] || len(f.Bytes) == 0 || len(f.Bytes) > maxCatalog-total {
			return "", false
		}
		total += len(f.Bytes)
		write([]byte(f.Name))
		write(f.Bytes)
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
func (r request) reply(outcome string) response { return response{r.binding, outcome} }

func readMessage(r io.Reader, dst any, limit int64) bool {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(data)) > limit {
		return false
	}
	// The wire shape is bounded (six flat file objects). Limit token count
	// before building the generic tree used to reject duplicates and case aliases.
	scan := json.NewDecoder(bytes.NewReader(data))
	for i := 0; ; i++ {
		if i > 80 {
			return false
		}
		_, err := scan.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
	}
	value, issue := jsonvalue.Parse(data)
	obj, ok := value.(map[string]any)
	if issue != nil || !ok || len(obj) != 4 || obj["Attempt"] == nil || obj["Profile"] == nil || obj["Digest"] == nil {
		return false
	}
	switch dst.(type) {
	case *response:
		if obj["Outcome"] == nil {
			return false
		}
	case *request:
		fs, ok := obj["Files"].([]any)
		if !ok || len(fs) < 5 || len(fs) > 6 {
			return false
		}
		for _, item := range fs {
			m, ok := item.(map[string]any)
			if !ok || len(m) != 2 || m["Name"] == nil || m["Bytes"] == nil {
				return false
			}
		}
	default:
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(dst) == nil
}
func decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	if !readMessage(r.Body, dst, limit) {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}
func send(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}
