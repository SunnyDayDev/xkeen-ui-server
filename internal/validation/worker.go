package validation

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type executor interface {
	check(context.Context, request) error
}
type handler struct {
	execute executor
	slot    chan struct{}
}

func newHandler(e executor) *handler { return &handler{e, make(chan struct{}, 1)} }
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != endpoint || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var req request
	if !decode(w, r, &req, maxBody) {
		return
	}
	nonce, err := hex.DecodeString(req.Attempt)
	digest, ok := req.manifest()
	if err != nil || len(nonce) != 32 || !ok || req.Digest != digest {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	err = h.execute.check(ctx, req)
	outcome := "unavailable"
	if ctx.Err() == nil {
		switch {
		case err == nil:
			outcome = "ok"
		case errors.Is(err, publication.ErrCatalogRejected):
			outcome = "rejected"
		}
	}
	send(w, req.reply(outcome))
}

// WorkerTLS restricts requests to one operator-selected client leaf in addition to normal PKI checks.
func WorkerTLS(identity tls.Certificate, ca *x509.CertPool, clientSHA256 string) (*tls.Config, error) {
	pin, err := hex.DecodeString(clientSHA256)
	if err != nil || len(pin) != 32 || ca == nil || len(identity.Certificate) == 0 {
		return nil, publication.ErrValidatorUnavailable
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity}, ClientCAs: ca, ClientAuth: tls.RequireAndVerifyClientCert, VerifyConnection: func(s tls.ConnectionState) error {
		if len(s.VerifiedChains) == 0 || len(s.PeerCertificates) == 0 {
			return publication.ErrValidatorUnavailable
		}
		sum := sha256.Sum256(s.PeerCertificates[0].Raw)
		if hex.EncodeToString(sum[:]) != hex.EncodeToString(pin) {
			return publication.ErrValidatorUnavailable
		}
		return nil
	}}, nil
}
