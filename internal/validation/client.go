package validation

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type client struct {
	url  string
	http *http.Client
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
func newClient(address string, t *http.Transport) publication.CatalogValidator {
	return &client{address, &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: noRedirect}}
}

// Local selects a private worker Unix socket.
func Local(socket string) (publication.CatalogValidator, error) {
	canonical, err := filepath.EvalSymlinks(filepath.Dir(socket))
	if err != nil || !filepath.IsAbs(socket) {
		return nil, publication.ErrValidatorUnavailable
	}
	socket = filepath.Join(canonical, filepath.Base(socket))
	if !privateSocket(socket) {
		return nil, publication.ErrValidatorUnavailable
	}
	t := &http.Transport{DisableCompression: true, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if !privateSocket(socket) {
			return nil, publication.ErrValidatorUnavailable
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return newClient("http://unix"+endpoint, t), nil
}

// Trust only a socket placed by the operator in a protected directory tree.
func privateSocket(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0002 != 0 {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	for first := true; ; first = false {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (st.Uid != 0 && int(st.Uid) != os.Geteuid()) {
			return false
		}
		if info.Mode().Perm()&0022 != 0 && (first || info.Mode()&os.ModeSticky == 0) {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return true
		}
		dir = parent
	}
}

// Remote selects one explicit HTTPS origin. Use a dedicated CA and client certificate.
func Remote(origin string, config *tls.Config) (publication.CatalogValidator, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || config == nil || config.InsecureSkipVerify || config.RootCAs == nil || len(config.Certificates) != 1 {
		return nil, publication.ErrValidatorUnavailable
	}
	// Construct a transport without ambient proxies, identity overrides or callbacks.
	tlsConfig := &tls.Config{RootCAs: config.RootCAs.Clone(), Certificates: config.Certificates, MinVersion: tls.VersionTLS13}
	return newClient("https://"+u.Host+endpoint, &http.Transport{TLSClientConfig: tlsConfig, DisableCompression: true}), nil
}
func (c *client) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	if profile != configuration.XrayIdentityVersion {
		return publication.ErrValidatorUnavailable
	}
	req, err := newRequest(profile, files)
	if err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return publication.ErrValidatorUnavailable
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return publication.ErrValidatorUnavailable
	}
	httpReq.Header.Set("Content-Type", "application/json")
	result, err := c.http.Do(httpReq)
	if err != nil {
		return publication.ErrValidatorUnavailable
	}
	defer result.Body.Close()
	var reply response
	if result.StatusCode != http.StatusOK || !readMessage(result.Body, &reply, 2048) || reply.binding != req.binding {
		return publication.ErrValidatorUnavailable
	}
	switch reply.Outcome {
	case "ok":
		return nil
	case "rejected":
		return publication.ErrCatalogRejected
	default:
		return publication.ErrValidatorUnavailable
	}
}
