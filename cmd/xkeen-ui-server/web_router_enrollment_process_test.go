package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessWebRouterEnrollment(t *testing.T) {
	agent := buildAgentForProcessTest(t)
	dir := t.TempDir()
	database := filepath.Join(dir, "server.sqlite")
	_, credentials := fixtureEnrollment(t, database)
	const elementID = "550e8400-e29b-41d4-a716-446655440000"
	const usedInbound = "550e8400-e29b-41d4-a716-446655440011"
	const freeInbound = "550e8400-e29b-41d4-a716-446655440012"
	const localInbound = "550e8400-e29b-41d4-a716-446655440013"
	db, err := sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	draft := []byte(`[{"name":"outbounds","elements":[{"disabled":false,"record":{"id":"` + elementID + `","content":"{\"tag\":\"example\",\"test\":\"$` + "{port}" + `\"}","variables":[{"name":"port","type":"number","default":443}]}}]},
		{"name":"inbounds","elements":[
		{"disabled":false,"record":{"id":"` + usedInbound + `","content":"{\"tag\":\"used-inbound\"}","variables":[]}},
		{"disabled":false,"record":{"id":"` + freeInbound + `","content":"{\"tag\":\"free-inbound\"}","variables":[]}}]}]`)
	if _, err := db.ExecContext(t.Context(), "UPDATE accepted_templates SET draft=?", draft); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := credentials.Revoke(t.Context(), "router-example"); err != nil {
		t.Fatal(err)
	}
	const password = "synthetic enrollment password"
	passwordPath := filepath.Join(dir, "password")
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runBinaryInDir(t, dir, "admin", "setup", "--db-path", database, "--password-file", passwordPath); code != 0 {
		t.Fatal("administrator setup failed")
	}
	cert, key, roots := fixtureServerCertificate(t)
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	origin := "https://" + listen
	server := startProcess(t, testBinary, "--listen", listen, "--db-path", database,
		"--tls-cert", cert, "--tls-key", key, "--web-origin", origin, "--agent-origin", origin)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	server.readyWithClient(t, client, "https")
	var session *http.Cookie
	var csrf string
	request := func(method, path string, payload []byte, want int) ([]byte, *http.Response) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		if session != nil {
			req.AddCookie(session)
		}
		if method == http.MethodPost || method == http.MethodPatch {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d error=%v", method, path, res.StatusCode, err)
		}
		return raw, res
	}
	const enrollment = "/ui/v1/routers/enroll?routerId=router-example"
	const overview = "/ui/v1/routers"
	request(http.MethodGet, overview, nil, http.StatusUnauthorized)
	request(http.MethodGet, enrollment, nil, http.StatusUnauthorized)
	const catalog = "/ui/v1/routers/catalog?routerId=router-example&source="
	request(http.MethodGet, catalog+"personal", nil, http.StatusUnauthorized)
	const variablePath = "/ui/v1/routers/variables?routerId=router-example&elementId=" + elementID
	request(http.MethodGet, variablePath, nil, http.StatusUnauthorized)
	login, _ := json.Marshal(map[string]string{"password": password})
	body, res := request(http.MethodPost, "/ui/v1/login", login, http.StatusOK)
	var signedIn struct{ CSRF string }
	if json.Unmarshal(body, &signedIn) != nil || signedIn.CSRF == "" || len(res.Cookies()) != 1 {
		t.Fatal("missing administrator session")
	}
	session, csrf = res.Cookies()[0], signedIn.CSRF
	if raw, _ := request(http.MethodGet, overview, nil, http.StatusOK); !bytes.Equal(bytes.TrimSpace(raw), []byte(`{"routers":[]}`)) {
		t.Fatal("unobserved router entered the overview")
	}
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	var before struct{ Origin, Epoch, Phase, DesiredRevision, Contact string }
	if json.Unmarshal(body, &before) != nil || before.Origin != origin || before.Phase != "missing" ||
		before.Contact != "unobserved" || before.DesiredRevision != "1" {
		t.Fatal("unexpected initial enrollment state")
	}
	intent, _ := json.Marshal(map[string]string{"action": "issue", "epoch": before.Epoch, "desiredRevision": before.DesiredRevision})
	body, _ = request(http.MethodPost, enrollment, intent, http.StatusCreated)
	var receipt struct {
		Invitation string
		Expires    time.Time
	}
	if json.Unmarshal(body, &receipt) != nil || receipt.Invitation == "" || time.Until(receipt.Expires) < 14*time.Minute {
		t.Fatal("incomplete invitation receipt")
	}
	request(http.MethodPost, enrollment, intent, http.StatusConflict)
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	if bytes.Contains(body, []byte(receipt.Invitation)) || !bytes.Contains(body, []byte(`"phase":"pending"`)) {
		t.Fatal("unsafe pending metadata")
	}
	agentState := filepath.Join(dir, "agent")
	if err := os.Mkdir(agentState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentState, "invitation"), []byte(receipt.Invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := startProcess(t, agent, "--server", origin, "--state-dir", agentState, "--ca-file", cert)
	waitForAgentAuthentication(t, worker)
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, _ = request(http.MethodGet, overview, nil, http.StatusOK)
		if bytes.Contains(body, []byte(`"routerId":"router-example"`)) && bytes.Contains(body, []byte(`"contact":"online"`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted contact did not reach the protected overview")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if bytes.Contains(body, []byte(receipt.Invitation)) || bytes.Contains(body, []byte(`"appliedRevision"`)) {
		t.Fatal("overview exposed an invitation or fabricated applied evidence")
	}
	desiredBefore, _ := request(http.MethodGet, catalog+"desired", nil, http.StatusOK)
	if !bytes.Contains(desiredBefore, []byte(`"desiredRevision":"1"`)) {
		t.Fatal("desired revision identity missing")
	}
	request(http.MethodGet, catalog+"desired&source=personal", nil, http.StatusBadRequest)
	body, _ = request(http.MethodGet, variablePath, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"defaultSource":"443"`)) || !bytes.Contains(body, []byte(`"draftGeneration":"0"`)) {
		t.Fatal("snapshot did not show the chosen variables and original generation")
	}
	source := " 9007199254740993\r\n"
	values := map[string]any{"templateId": "template-example", "activeGeneration": "1", "acceptedVersion": "1", "draftGeneration": "0",
		"values": []map[string]string{{"name": "port", "source": source}}}
	literal, _ := json.Marshal(values)
	body, _ = request(http.MethodPost, variablePath, literal, http.StatusOK)
	if !bytes.Contains(body, []byte(`"draftGeneration":"1"`)) || !bytes.Contains(body, []byte(`"personalSource":" 9007199254740993\r\n"`)) {
		t.Fatal("exact accepted personal source missing")
	}
	personalPreview, _ := request(http.MethodGet, catalog+"personal", nil, http.StatusOK)
	var previewFiles struct {
		Source string
		Files  []struct {
			Name    string
			Content []byte
		}
	}
	if err := json.Unmarshal(personalPreview, &previewFiles); err != nil || previewFiles.Source != "personal" {
		t.Fatalf("preview structure %v", err)
	}
	var outbound []byte
	for _, file := range previewFiles.Files {
		if file.Name == "04_outbounds.json" {
			outbound = file.Content
		}
	}
	var actual struct{ Outbounds []map[string]json.RawMessage }
	if json.Unmarshal(outbound, &actual) != nil || len(actual.Outbounds) != 1 ||
		string(actual.Outbounds[0]["test"]) != "9007199254740993" {
		t.Fatalf("missing exact assembled value: %s", outbound)
	}
	desiredAfter, _ := request(http.MethodGet, catalog+"desired", nil, http.StatusOK)
	if !bytes.Equal(desiredBefore, desiredAfter) {
		t.Fatal("catalog preview changed the desired snapshot")
	}
	brokenDraft := bytes.Replace(draft, []byte(`\"tag\":\"example\"`), []byte(`\"tag\":false`), 1)
	if bytes.Equal(brokenDraft, draft) {
		t.Fatal("broken B12 fixture unchanged")
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE accepted_templates SET draft=?", brokenDraft); err != nil {
		t.Fatal(err)
	}
	failedPreview, _ := request(http.MethodGet, catalog+"personal", nil, http.StatusUnprocessableEntity)
	if !bytes.Contains(failedPreview, []byte(`"state":"catalog_unready"`)) ||
		!bytes.Contains(failedPreview, []byte(`"issues":[`)) || bytes.Contains(failedPreview, []byte(`"files"`)) {
		t.Fatalf("unexpected assembly failure: %s", failedPreview)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE accepted_templates SET draft=?", draft); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, variablePath, literal, http.StatusConflict)
	values["draftGeneration"] = "1"
	values["values"] = []map[string]string{{"name": "port"}}
	inherited, _ := json.Marshal(values)
	body, _ = request(http.MethodPost, variablePath, inherited, http.StatusOK)
	if !bytes.Contains(body, []byte(`"draftGeneration":"2"`)) || bytes.Contains(body, []byte(`"personalSource"`)) {
		t.Fatal("explicit inheritance did not remove the stored personal source")
	}
	values["draftGeneration"] = "2"
	values["values"] = []map[string]string{{"name": "port", "source": "9"}}
	personal, _ := json.Marshal(values)
	request(http.MethodPost, variablePath, personal, http.StatusOK)
	const replacementPath = "/ui/v1/routers/replacement?routerId=router-example&elementId=" + elementID
	body, _ = request(http.MethodGet, replacementPath, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"mode":"variables"`)) || bytes.Contains(body, []byte(`"source"`)) {
		t.Fatal("inherited editor exposed a replacement")
	}
	replacement := map[string]string{"templateId": "template-example", "activeGeneration": "1", "acceptedVersion": "1", "draftGeneration": "3", "action": "replace", "source": "malformed"}
	change := func(want int) []byte {
		raw, _ := json.Marshal(replacement)
		body, _ := request(http.MethodPost, replacementPath, raw, want)
		return body
	}
	change(http.StatusUnprocessableEntity)
	literalReplacement := " \r\n{\"v\":\"$" + "{port}\"}\n"
	replacement["source"] = literalReplacement
	body = change(http.StatusOK)
	var storedReplacement struct {
		Source    string
		ElementID string
	}
	if json.Unmarshal(body, &storedReplacement) != nil || storedReplacement.Source != literalReplacement || storedReplacement.ElementID != elementID {
		t.Fatal("replacement was interpreted or identity changed")
	}
	change(http.StatusConflict)
	replacement["draftGeneration"] = "4"
	replacement["action"] = "variables"
	delete(replacement, "source")
	change(http.StatusOK)
	body, _ = request(http.MethodGet, variablePath, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"personalSource":"9"`)) {
		t.Fatal("return lost the previous personal value")
	}
	replacement["draftGeneration"] = "5"
	replacement["action"] = "reset"
	change(http.StatusOK)
	body, _ = request(http.MethodGet, variablePath, nil, http.StatusOK)
	if bytes.Contains(body, []byte(`"personalSource"`)) {
		t.Fatal("full reset retained the personal value")
	}
	const rulePath = "/ui/v1/routers/rules?routerId=router-example"
	body, _ = request(http.MethodGet, rulePath, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"rules":[]`)) {
		t.Fatal("empty full rule order did not round-trip")
	}
	ruleSource := " \r\n{\"ruleTag\":\"new-rule\",\"weight\":9007199254740993,\"inboundTag\":42}\n"
	insertRule, _ := json.Marshal(map[string]string{"templateId": "template-example", "activeGeneration": "1", "acceptedVersion": "1",
		"draftGeneration": "6", "afterId": "", "source": ruleSource})
	body, _ = request(http.MethodPost, rulePath, insertRule, http.StatusOK)
	var ordered struct {
		DraftGeneration string
		Rules           []struct{ ID, Origin string }
	}
	if json.Unmarshal(body, &ordered) != nil || ordered.DraftGeneration != "7" || len(ordered.Rules) != 1 || ordered.Rules[0].Origin != "local" {
		t.Fatal("first local rule did not appear in the full order")
	}
	request(http.MethodPost, rulePath, insertRule, http.StatusConflict)
	var ruleBlob []byte
	if err := db.QueryRowContext(t.Context(), "SELECT snapshot FROM router_settings").Scan(&ruleBlob); err != nil ||
		!bytes.Contains(ruleBlob, []byte(base64.StdEncoding.EncodeToString([]byte(ruleSource)))) {
		t.Fatal("stored rule did not retain exact bytes")
	}
	firstLocal := ordered.Rules[0].ID
	secondInsert, _ := json.Marshal(map[string]string{"templateId": "template-example", "activeGeneration": "1", "acceptedVersion": "1",
		"draftGeneration": "7", "afterId": firstLocal, "source": `{"ruleTag":"second-local","inboundTag":["used-inbound"]}`})
	body, _ = request(http.MethodPost, rulePath, secondInsert, http.StatusOK)
	if json.Unmarshal(body, &ordered) != nil || ordered.DraftGeneration != "8" || len(ordered.Rules) != 2 {
		t.Fatal("second local rule missing")
	}
	secondLocal := ordered.Rules[1].ID
	basisJSON := `{"templateId":"template-example","activeGeneration":"1","acceptedVersion":"1",`
	move := []byte(basisJSON + `"draftGeneration":"8","action":"move","ruleId":"` + secondLocal + `","position":0}`)
	resetWithoutCSRF := csrf
	csrf = ""
	request(http.MethodPatch, rulePath, move, http.StatusForbidden)
	csrf = resetWithoutCSRF
	request(http.MethodPatch, rulePath, []byte(basisJSON+`"draftGeneration":"8","action":"reset","ruleId":null}`), http.StatusBadRequest)
	body, _ = request(http.MethodPatch, rulePath, move, http.StatusOK)
	if json.Unmarshal(body, &ordered) != nil || ordered.DraftGeneration != "9" || ordered.Rules[0].ID != secondLocal {
		t.Fatal("move did not change the full order")
	}
	request(http.MethodPatch, rulePath, move, http.StatusConflict)
	reset := []byte(basisJSON + `"draftGeneration":"9","action":"reset"}`)
	body, _ = request(http.MethodPatch, rulePath, reset, http.StatusOK)
	if json.Unmarshal(body, &ordered) != nil || ordered.DraftGeneration != "10" || ordered.Rules[0].ID != secondLocal || ordered.Rules[1].ID != firstLocal {
		t.Fatal("template reset moved positional local rules")
	}
	disabled, _ := json.Marshal([]string{firstLocal})
	localGroup := `{"Name":"inbounds","Local":[{"ID":"` + localInbound + `","Content":"` +
		base64.StdEncoding.EncodeToString([]byte(`{"tag":"local-inbound"}`)) + `"}],"Overrides":null,"Exclusions":null}`
	if _, err := db.ExecContext(t.Context(), `UPDATE router_settings
		SET snapshot=json_insert(json_set(CAST(snapshot AS TEXT), '$.DisabledRules', json(?)), '$.Groups[#]', json(?)),
			generation=generation+1 WHERE id='router-example'`, string(disabled), localGroup); err != nil {
		t.Fatal(err)
	}
	const inboundsPath = "/ui/v1/routers/inbounds?routerId=router-example&elementId="
	body, _ = request(http.MethodGet, inboundsPath+usedInbound, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"tag":"used-inbound"`)) ||
		!bytes.Contains(body, []byte(`"ruleId":"`+secondLocal+`"`)) || bytes.Contains(body, []byte(`"weight"`)) {
		t.Fatalf("dependency read did not return the exact effective rule: %s", body)
	}
	changeInbound := func(id, generation, action string, want int) []byte {
		raw := []byte(basisJSON + `"draftGeneration":"` + generation + `","action":"` + action + `"}`)
		body, _ := request(http.MethodPost, inboundsPath+id, raw, want)
		return body
	}
	for _, action := range []string{"disable", "remove"} {
		blocked := changeInbound(usedInbound, "11", action, http.StatusConflict)
		if !bytes.Contains(blocked, []byte(`"state":"inbound_in_use"`)) ||
			!bytes.Contains(blocked, []byte(`"ruleId":"`+secondLocal+`"`)) {
			t.Fatalf("used inbound was not guarded: %s", blocked)
		}
	}
	changeInbound(freeInbound, "11", "disable", http.StatusOK)
	changeInbound(freeInbound, "11", "remove", http.StatusConflict)
	changeInbound(freeInbound, "12", "remove", http.StatusOK)
	request(http.MethodGet, inboundsPath+freeInbound, nil, http.StatusNotFound)
	changeInbound(localInbound, "13", "disable", http.StatusOK)
	changeInbound(localInbound, "14", "remove", http.StatusOK)
	request(http.MethodGet, inboundsPath+localInbound, nil, http.StatusNotFound)
	var stored, storedOrder []byte
	if err := db.QueryRowContext(t.Context(), "SELECT snapshot, order_basis FROM router_settings WHERE id='router-example'").Scan(&stored, &storedOrder); err != nil ||
		!bytes.Contains(stored, []byte(`"Exclusions":[{"TemplateID":"template-example","ElementID":"`+freeInbound+`"}]`)) ||
		bytes.Contains(stored, []byte(localInbound)) || storedOrder == nil {
		t.Fatalf("inbound change rewrote source, local data or ordering: %s / %v", stored, err)
	}
	var desired int
	if err := db.QueryRowContext(t.Context(), "SELECT max(revision) FROM desired_router_revisions").Scan(&desired); err != nil || desired != 1 {
		t.Fatal("editing changed the desired revision")
	}
	stopRouterAgent(t, worker)
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	if bytes.Contains(body, []byte(receipt.Invitation)) || !bytes.Contains(body, []byte(`"phase":"registered"`)) ||
		bytes.Contains(body, []byte(`"appliedRevision"`)) {
		t.Fatal("registration unexpectedly exposed invitation or application")
	}
	if strings.Contains(server.output(), receipt.Invitation) || strings.Contains(worker.output(), receipt.Invitation) {
		t.Fatal("invitation exposed in process output")
	}
	readKey := func(path string) string {
		t.Helper()
		store, err := statefile.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		binding, err := store.Load()
		if err != nil || binding == nil || binding.RouterID != "router-example" || binding.Key == "" {
			t.Fatal("agent did not persist exact router identity")
		}
		return binding.Key
	}
	agentRequest := func(method, path, key, body string, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s got %d: %v", method, path, res.StatusCode, err)
		}
		return out
	}
	oldKey := readKey(agentState)
	const contact = "/agent/v1/contact"
	agentRequest(http.MethodPost, contact, oldKey, `{"report":{"sequence":"7","revision":"1","state":"applied"}}`, 204)
	action := func(name string, want int) string {
		t.Helper()
		state, _ := request(http.MethodGet, enrollment, nil, 200)
		var coordinates struct{ Epoch, DesiredRevision string }
		if json.Unmarshal(state, &coordinates) != nil {
			t.Fatal("missing coordinates")
		}
		intent, _ := json.Marshal(map[string]string{"action": name, "epoch": coordinates.Epoch, "desiredRevision": coordinates.DesiredRevision})
		raw, _ := request(http.MethodPost, "/ui/v1/routers/keys?routerId=router-example", intent, want)
		var receipt struct{ Invitation string }
		if json.Unmarshal(raw, &receipt) != nil || (want == 201) != (receipt.Invitation != "") {
			t.Fatal("invalid key operation receipt")
		}
		return receipt.Invitation
	}
	startReplacement := func(label, token string) string {
		t.Helper()
		dir := filepath.Join(dir, label)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "invitation"), []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		candidate := startProcess(t, agent, "--server", origin, "--expected-router-id", "router-example", "--state-dir", dir, "--ca-file", cert)
		waitForAgentAuthentication(t, candidate)
		stopRouterAgent(t, candidate)
		if strings.Contains(candidate.output(), token) {
			t.Fatal("agent logs include a secret")
		}
		return readKey(dir)
	}
	rotation := action("rotate", 201)
	agentRequest(http.MethodGet, "/agent/v1/identity", oldKey, "", 200)
	newKey := startReplacement("rotated", rotation)
	if newKey == oldKey {
		t.Fatal("rotation reused a working secret")
	}
	agentRequest(http.MethodGet, "/agent/v1/identity", oldKey, "", 401)
	if raw := agentRequest(http.MethodGet, contact, newKey, "", 200); !bytes.Contains(raw, []byte(`"sequence":"7"`)) {
		t.Fatal("rotated key lost report cursor")
	}
	pending := action("rotate", 201)
	action("revoke", 200)
	agentRequest(http.MethodGet, "/agent/v1/identity", newKey, "", 401)
	redeem, _ := json.Marshal(map[string]string{"credential": agentcredential.Generate(agentcredential.Working).Encode()})
	agentRequest(http.MethodPost, "/agent/v1/enroll", pending, string(redeem), 401)
	recovery := action("recover", 201)
	recovered := startReplacement("recovered", recovery)
	if raw := agentRequest(http.MethodGet, contact, recovered, "", 200); !bytes.Contains(raw, []byte(`"sequence":"7"`)) {
		t.Fatal("recovered key lost report cursor")
	}
	log := server.output()
	for _, token := range []string{oldKey, newKey, recovered, rotation, pending, recovery} {
		if strings.Contains(log, token) {
			t.Fatal("agent key action audit includes a secret")
		}
	}
	for _, action := range []string{"rotate", "revoke", "recover"} {
		if !strings.Contains(log, `"action":"`+action+`"`) {
			t.Fatal("missing agent key audit event")
		}
	}

}
