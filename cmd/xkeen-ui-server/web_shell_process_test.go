package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestProcessWebShellAndAssets(t *testing.T) {
	cert, key, roots := fixtureServerCertificate(t)
	const host = "console.example"
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", filepath.Join(t.TempDir(), "server.db"), "--tls-cert", cert, "--tls-key", key, "--web-origin", "https://"+host)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	addr := p.readyWithClient(t, client, "https")
	read := func(authority, path string, status int) (http.Header, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status {
			t.Fatalf("%s: status=%d err=%v", path, res.StatusCode, err)
		}
		return res.Header, string(body)
	}
	headings, body := read(host, "/ui/", 200)
	if headings.Get("Cache-Control") != "no-store" || !strings.Contains(body, "XKeen Console") {
		t.Fatal("expected uncached console HTML")
	}
	asset := regexp.MustCompile(`src="(/ui/assets/[^"]+\.js)"`).FindStringSubmatch(body)
	if len(asset) != 2 {
		t.Fatal("missing embedded script")
	}
	h, body := read(host, asset[1], 200)
	if !strings.Contains(h.Get("Content-Type"), "javascript") || len(body) == 0 {
		t.Fatal("invalid script asset")
	}
	read("wrong.example", "/ui/", 403)
	read(host, "/ui/templates", 200)
	read(host, "/ui/v1/templates", 401)
	read(host, "/ui/assets/missing.js", 404)
	read(host, "/ui/v1/not-found", 404)
	_, body = read(host, "/ui/v1/session", 200)
	if !strings.Contains(body, "setup_required") {
		t.Fatal("missing F03 initial status")
	}
}
