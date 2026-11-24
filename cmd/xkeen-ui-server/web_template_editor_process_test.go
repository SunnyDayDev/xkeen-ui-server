package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessWebTemplateEditor(t *testing.T) {
	const authority = "admin.example"
	const origin = "https://" + authority
	const id = "exact / # Шаблон "
	dir := t.TempDir()
	path := filepath.Join(dir, "server.db")
	secret := filepath.Join(dir, "password")
	if err := os.WriteFile(secret, []byte("synthetic editor password"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runBinaryInDir(t, dir, "admin", "setup", "--db-path", path, "--password-file", secret); code != 0 {
		t.Fatal(stderr)
	}
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key, "--web-origin", origin)
	base := "https://" + p.readyWithClient(t, client, "https")
	defer stopAgentProcess(t, p)
	var cookie *http.Cookie
	var csrf string
	request := func(method, path string, input any, want int) (map[string]any, []*http.Cookie) {
		t.Helper()
		raw := ""
		if input != nil {
			data, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(data)
		}
		r, err := http.NewRequestWithContext(t.Context(), method, base+path, strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = authority
		if method == http.MethodPost {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s => %d, want %d", method, path, response.StatusCode, want)
		}
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body, response.Cookies()
	}
	target := "/ui/v1/templates/item?templateId=" + url.QueryEscape(id)
	request("GET", target, nil, 401)
	request("POST", target, "synthetic invalid envelope", 401)
	login, cookies := request("POST", "/ui/v1/login", map[string]string{"password": "synthetic editor password"}, 200)
	cookie = cookies[0]
	csrf = login["csrf"].(string)
	request("GET", target, nil, 404)
	request("POST", "/ui/v1/templates", map[string]string{"id": id}, 201)
	empty, _ := request("GET", target, nil, 200)
	if empty["version"] != "1" || empty["id"] != id || len(empty["firstSections"].([]any)) == 0 {
		t.Fatal("empty draft metadata")
	}
	raw := "{\r\n \"n\":9007199254740993, \"слово\":\"\u00e9\"\n}\r\n"
	input := map[string]string{"version": "1", "section": "outbounds", "source": raw}
	saved, _ := request("POST", target, input, 200)
	element := saved["selected"].(map[string]any)
	if saved["version"] != "2" || element["source"] != raw {
		t.Fatal("exact source not returned")
	}
	request("POST", target, input, 409)
	elementID := element["id"].(string)
	input = map[string]string{"version": "2", "elementId": elementID, "source": `{"invalid":}`}
	bad, _ := request("POST", target, input, 422)
	if bad["state"] != "invalid_source" || bad["offset"] == nil {
		t.Fatal("missing error position")
	}
	read, _ := request("GET", target+"&elementId="+elementID, nil, 200)
	if read["version"] != "2" || read["selected"].(map[string]any)["source"] != raw {
		t.Fatal("rejected edit changed stored text")
	}
	badCSRF := csrf
	csrf = "invalid"
	input["source"] = `{"tag":"next.example"}`
	request("POST", target, input, 403)
	csrf = badCSRF
	request("POST", target, input, 200)
}
