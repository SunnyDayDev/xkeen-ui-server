package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessWebRouterPreparation(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	database := filepath.Join(dir, "server.sqlite")
	passwordFile := filepath.Join(dir, "password")
	const password = "synthetic router setup password"
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, status := runBinaryInDir(t, dir, "admin", "setup", "--db-path", database, "--password-file", passwordFile)
	if status != 0 {
		t.Fatal("administrator setup failed")
	}
	databaseHandle, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer databaseHandle.Close()
	const templateID = "edge / example"
	const element = "550e8400-e29b-41d4-a716-446655440000"
	_, err = templates.New(sqlite.NewTemplateRepository(databaseHandle)).Create(ctx, configuration.TemplateDraft{ID: templateID, Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: element, Content: `{"protocol":"${protocol}","tag":"${label}"}`,
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	cert, key, roots := fixtureServerCertificate(t)
	const authority = "admin.example"
	const origin = "https://" + authority
	socket := os.Getenv("XKEEN_TEST_F09_VALIDATOR_SOCKET")
	remote := os.Getenv("XKEEN_TEST_F09_VALIDATOR_ORIGIN")
	args := []string{"--listen", "127.0.0.1:0", "--db-path", database, "--tls-cert", cert, "--tls-key", key, "--web-origin", origin}
	if socket != "" {
		args = append(args, "--validator-socket", socket)
	} else if remote != "" {
		trust := os.Getenv("XKEEN_TEST_F09_VALIDATOR_TRUST")
		args = append(args, "--validator-origin", remote, "--validator-ca", filepath.Join(trust, "worker-ca.pem"),
			"--validator-client-cert", filepath.Join(trust, "client.pem"), "--validator-client-key", filepath.Join(trust, "client.key"))
	}
	p := startProcess(t, testBinary, args...)
	client := &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	addr := p.readyWithClient(t, client, "https")
	var session *http.Cookie
	var csrf string
	var externalBearer string
	request := func(method, path string, payload []byte, status int) ([]byte, *http.Response) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
		if session != nil {
			req.AddCookie(session)
		}
		if method == http.MethodPost {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if externalBearer != "" && strings.HasPrefix(path, "/api/v1/") {
			req.Header.Set("Authorization", "Bearer "+externalBearer)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s => %d, %q, %v", method, path, res.StatusCode, raw, err)
		}
		return raw, res
	}
	const unpublishedID = "unassigned / example"
	if _, err := templates.New(sqlite.NewTemplateRepository(databaseHandle)).Create(ctx, configuration.TemplateDraft{ID: unpublishedID}); err != nil {
		t.Fatal(err)
	}
	publicationPath := "/ui/v1/templates/publication?templateId=" + url.QueryEscape(unpublishedID)
	request(http.MethodGet, publicationPath, nil, 401)
	request(http.MethodGet, "/ui/v1/routers/profile?routerId=router-example&targetTemplateId="+url.QueryEscape(unpublishedID), nil, 401)
	const route = "/ui/v1/routers/prepare"
	selectedRoute := route + "?templateId=" + url.QueryEscape(templateID)
	request(http.MethodGet, selectedRoute, nil, 401)
	login, _ := json.Marshal(map[string]string{"password": password})
	raw, res := request(http.MethodPost, "/ui/v1/login", login, 200)
	var loginResult struct{ CSRF string }
	if json.Unmarshal(raw, &loginResult) != nil || loginResult.CSRF == "" || len(res.Cookies()) != 1 {
		t.Fatal("missing verified session")
	}
	csrf, session = loginResult.CSRF, res.Cookies()[0]
	initial, _ := request(http.MethodGet, publicationPath, nil, 200)
	if !strings.Contains(string(initial), `"acceptedVersion":"0"`) {
		t.Fatalf("unexpected initial publication: %s", initial)
	}
	request(http.MethodPost, publicationPath, []byte(`{"action":"preview","version":"1","receipt":null}`), 400)
	checked, _ := request(http.MethodPost, publicationPath, []byte(`{"action":"preview","version":"1"}`), 200)
	var result struct {
		Receipt string
		Members []any
	}
	if json.Unmarshal(checked, &result) != nil || result.Receipt == "" || result.Members == nil || len(result.Members) != 0 {
		t.Fatalf("missing zero-member preview: %s", checked)
	}
	beforeConfirmation, _ := request(http.MethodGet, publicationPath, nil, 200)
	if !bytes.Equal(initial, beforeConfirmation) {
		t.Fatalf("preview changed state: %s", beforeConfirmation)
	}
	request(http.MethodPost, publicationPath, []byte(`{"action":"confirm","receipt":"invalid"}`), 409)
	confirmation, _ := json.Marshal(map[string]string{"action": "confirm", "receipt": result.Receipt})
	committed, _ := request(http.MethodPost, publicationPath, confirmation, 200)
	if !strings.Contains(string(committed), `"acceptedVersion":"1"`) || !strings.Contains(string(committed), `"members":[]`) {
		t.Fatalf("unexpected zero-member commit: %s", committed)
	}
	request(http.MethodPost, publicationPath, confirmation, 409)
	owner, _, err := webadmin.SessionProof(session.Value, "", false)
	if err != nil {
		t.Fatal(err)
	}
	external := sqlite.NewExternalAccess(databaseHandle, time.Now)
	policy, _, err := external.Owner(ctx, owner, &externalaccess.Change{Version: 1, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	apiPreview := "/api/v1/templates/publication/preview?templateId="
	emptyEndpoint := apiPreview + url.QueryEscape(unpublishedID)
	previewVersion := []byte(`{"version":"1"}`)
	request(http.MethodPost, emptyEndpoint, previewVersion, 401)
	policy, externalBearer, err = external.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, emptyEndpoint, []byte(`{"version":"1","receipt":null}`), 400)
	empty, _ := request(http.MethodPost, emptyEndpoint, previewVersion, 200)
	if json.Unmarshal(empty, &result) != nil || result.Receipt == "" || len(result.Members) != 0 {
		t.Fatalf("invalid external empty preview: %s", empty)
	}
	forbidden, _ := json.Marshal(map[string]string{"action": "confirm", "receipt": result.Receipt})
	request(http.MethodPost, publicationPath, forbidden, 409)
	confirmEndpoint := "/api/v1/templates/publication/confirm?templateId="
	confirmPayload := func(receipt string) []byte {
		raw, err := json.Marshal(map[string]string{"receipt": receipt})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var webPlan struct{ Receipt string }
	if json.Unmarshal(checked, &webPlan) != nil || webPlan.Receipt == "" {
		t.Fatal("missing web proof")
	}
	request(http.MethodPost, confirmEndpoint+url.QueryEscape(unpublishedID), confirmPayload(webPlan.Receipt), 409)
	request(http.MethodPost, confirmEndpoint+url.QueryEscape(templateID), confirmPayload(result.Receipt), 409)
	emptyCommit, _ := request(http.MethodPost, confirmEndpoint+url.QueryEscape(unpublishedID), confirmPayload(result.Receipt), 200)
	if !strings.Contains(string(emptyCommit), `"acceptedVersion":"1"`) || !strings.Contains(string(emptyCommit), `"members":[]`) {
		t.Fatalf("zero-member confirmation: %s", emptyCommit)
	}
	if repeat, _ := request(http.MethodPost, confirmEndpoint+url.QueryEscape(unpublishedID), confirmPayload(result.Receipt), 200); !bytes.Equal(repeat, emptyCommit) {
		t.Fatalf("same empty plan grew an effect: %s", repeat)
	}

	request(http.MethodGet, "/ui/routers/connect", nil, 200)
	raw, _ = request(http.MethodGet, selectedRoute, nil, 200)
	var form struct{ RouterID, TemplateID, Source, Version string }
	if json.Unmarshal(raw, &form) != nil || !configuration.ValidElementID(form.RouterID) || form.TemplateID != templateID || form.Source != "draft" || form.Version != "1" ||
		!strings.Contains(string(raw), `"required":true`) {
		t.Fatalf("unexpected preparation form: %q", raw)
	}
	submit := func(protocol string, status int, state string) {
		t.Helper()
		values := []map[string]string{{"elementId": element, "name": "label", "source": `"example-direct"`}}
		if protocol != "" {
			values = append(values, map[string]string{"elementId": element, "name": "protocol", "source": "\"" + protocol + "\""})
		}
		payload, _ := json.Marshal(map[string]any{"routerId": form.RouterID, "templateId": templateID, "name": "Example router",
			"source": form.Source, "version": form.Version, "values": values})
		out, _ := request(http.MethodPost, route, payload, status)
		if state != "" && !strings.Contains(string(out), `"state":"`+state+`"`) {
			t.Fatalf("expected %s: %s", state, out)
		}
	}
	preparedRoute := route + "?routerId=" + url.QueryEscape(form.RouterID)
	submit("", 422, "invalid_input")
	request(http.MethodGet, preparedRoute, nil, 404)
	if socket == "" && remote == "" {
		submit("freedom", 503, "validator_unavailable")
		request(http.MethodGet, preparedRoute, nil, 404)
		return
	}
	submit("not-a-supported-protocol", 422, "catalog_rejected")
	request(http.MethodGet, preparedRoute, nil, 404)
	submit("freedom", 201, "")
	raw, _ = request(http.MethodGet, preparedRoute, nil, 200)
	if !strings.Contains(string(raw), `"revision":"1"`) {
		t.Fatalf("missing first revision: %s", raw)
	}
	saved, err := sqlite.NewPublications(databaseHandle).ReadDesired(ctx, form.RouterID)
	if err != nil || saved == nil || saved.Number != 1 {
		t.Fatalf("missing persisted first revision: %#v %v", saved, err)
	}
	found := false
	for _, file := range saved.Catalog.Files {
		found = found || strings.Contains(string(file.Content), "example-direct")
	}
	if !found {
		t.Fatal("personal value missing from desired catalog")
	}
	second := routersettings.Draft{ID: "offline.example", TemplateID: templateID, Name: "Inactive site"}
	store := sqlite.NewPublications(databaseHandle)
	if _, err := store.Commit(ctx, publication.CommitPlan{FirstConfirmation: true, Expected: publication.Expectations{RouterID: second.ID, TemplateID: second.TemplateID, AcceptedVersion: 1},
		Settings: second, NewRevision: fixtureDesiredCatalog("{}")}); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseHandle.ExecContext(ctx, `INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain)
		VALUES (?,?,NULL,0,0)`, second.ID, time.Now().Add(-181*time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRouterVisibility(databaseHandle).Hide(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	repo := templates.New(sqlite.NewTemplateRepository(databaseHandle))
	snapshot, err := repo.Get(ctx, templateID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Sections[0].Elements[0].Element.Content = `{"protocol":"blackhole","tag":"shared.example","unknownField":9007199254740993}`
	for i := range snapshot.Sections[0].Elements[0].Element.Variables {
		snapshot.Sections[0].Elements[0].Element.Variables[i].Default = json.RawMessage(`"default.example"`)
	}
	if _, err := repo.Replace(ctx, snapshot.TemplateDraft, 1); err != nil {
		t.Fatal(err)
	}
	realEndpoint := apiPreview + url.QueryEscape(templateID)
	actual, _ := request(http.MethodPost, realEndpoint, []byte(`{"version":"2"}`), 200)
	var members struct {
		Receipt string
		Members []struct {
			RouterID, Contact, DesiredRevision, Estimate string
			Changes                                      []struct{ Name, Kind string }
		}
	}
	if json.Unmarshal(actual, &members) != nil || len(members.Members) != 2 || members.Receipt == "" {
		t.Fatalf("full plan: %s", actual)
	}
	found = false
	for _, member := range members.Members {
		if member.DesiredRevision != "1" || len(member.Changes) == 0 {
			t.Fatalf("bad preview member: %+v", member)
		}
		if member.RouterID == second.ID && member.Contact == "offline" {
			found = true
		}
	}
	if !found {
		t.Fatal("hidden offline member excluded from read-only plan")
	}
	for _, id := range []string{form.RouterID, second.ID} {
		if saved, err := store.ReadDesired(ctx, id); err != nil || saved.Number != 1 {
			t.Fatalf("preview changed desired: %v %v", saved, err)
		}
	}
	snapshot.Sections[0].Elements[0].Element.Content = `{"protocol":"unsupported-example","tag":"shared.example"}`
	if _, err := repo.Replace(ctx, snapshot.TemplateDraft, 2); err != nil {
		t.Fatal(err)
	}
	refused, _ := request(http.MethodPost, realEndpoint, []byte(`{"version":"3"}`), 422)
	if !strings.Contains(string(refused), `"code":"catalog_rejected"`) || strings.Contains(string(refused), `"receipt"`) {
		t.Fatalf("partial Xray rejection: %s", refused)
	}
	confirmGroup := confirmEndpoint + url.QueryEscape(templateID)
	request(http.MethodPost, confirmGroup, confirmPayload(members.Receipt), 409)
	currentGroup, _ := request(http.MethodGet, "/api/v1/templates/publication?templateId="+url.QueryEscape(templateID), nil, 200)
	if !strings.Contains(string(currentGroup), `"acceptedVersion":"1"`) || !strings.Contains(string(currentGroup), `"state":"current"`) ||
		strings.Contains(string(currentGroup), `"receipt"`) || !strings.Contains(string(currentGroup), `"offline.example"`) {
		t.Fatalf("unsafe or incomplete recovery: %s", currentGroup)
	}
	for _, id := range []string{form.RouterID, second.ID} {
		if saved, err := store.ReadDesired(ctx, id); err != nil || saved.Number != 1 {
			t.Fatalf("stale confirmation changed desired: %v %v", saved, err)
		}
	}
	snapshot.Sections[0].Elements[0].Element.Content = `{"protocol":"blackhole","tag":"shared.example","unknownField":9007199254740993}`
	if _, err := repo.Replace(ctx, snapshot.TemplateDraft, 3); err != nil {
		t.Fatal(err)
	}
	actual, _ = request(http.MethodPost, realEndpoint, []byte(`{"version":"4"}`), 200)
	if json.Unmarshal(actual, &members) != nil || len(members.Members) != 2 || members.Receipt == "" {
		t.Fatalf("missing latest plan: %s", actual)
	}
	committedGroup, _ := request(http.MethodPost, confirmGroup, confirmPayload(members.Receipt), 200)
	var publicationResult struct {
		AcceptedVersion string
		Members         []struct {
			RouterID, DesiredRevision string
			Changed                   bool
		}
	}
	if json.Unmarshal(committedGroup, &publicationResult) != nil || publicationResult.AcceptedVersion != "4" ||
		len(publicationResult.Members) != 2 || strings.Contains(string(committedGroup), "appliedRevision") {
		t.Fatalf("group commit: %s", committedGroup)
	}
	for _, member := range publicationResult.Members {
		if member.DesiredRevision != "2" || !member.Changed {
			t.Fatalf("partial result: %+v", member)
		}
		saved, err := store.ReadDesired(ctx, member.RouterID)
		if err != nil || saved.Number != 2 {
			t.Fatalf("group desired: %v %v", saved, err)
		}
	}
	request(http.MethodPost, confirmGroup, confirmPayload(members.Receipt), 409)
	currentGroup, _ = request(http.MethodGet, "/api/v1/templates/publication?templateId="+url.QueryEscape(templateID), nil, 200)
	if !strings.Contains(string(currentGroup), `"acceptedVersion":"4"`) || !strings.Contains(string(currentGroup), `"state":"current"`) ||
		strings.Contains(string(currentGroup), `"receipt"`) || !strings.Contains(string(currentGroup), `"offline.example"`) {
		t.Fatalf("unsafe or incomplete recovery: %s", currentGroup)
	}

	const officeID = "office / example"
	const officeElement = "650e8400-e29b-41d4-a716-446655440000"
	if _, err := repo.Create(ctx, configuration.TemplateDraft{ID: officeID, Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: officeElement, Content: `{"protocol":"${officeProtocol}","tag":"${officeTag}"}`}}},
	}}}); err != nil {
		t.Fatal(err)
	}
	officePublication := "/ui/v1/templates/publication?templateId=" + url.QueryEscape(officeID)
	rawOffice, _ := request(http.MethodPost, officePublication, []byte(`{"action":"preview","version":"1"}`), 200)
	var officeReceipt struct{ Receipt string }
	if json.Unmarshal(rawOffice, &officeReceipt) != nil || officeReceipt.Receipt == "" {
		t.Fatal("missing office acceptance receipt")
	}
	acceptedOffice, _ := json.Marshal(map[string]string{"action": "confirm", "receipt": officeReceipt.Receipt})
	request(http.MethodPost, officePublication, acceptedOffice, 200)
	profilePath := "/ui/v1/routers/profile?routerId=" + url.QueryEscape(form.RouterID)
	officePath := profilePath + "&targetTemplateId=" + url.QueryEscape(officeID)
	request(http.MethodGet, officePath, nil, 404)
	if _, err := databaseHandle.ExecContext(ctx, "INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain) VALUES (?,?,NULL,0,0)", form.RouterID, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	readOffice, _ := request(http.MethodGet, officePath, nil, 200)
	var office struct {
		ActiveTemplateID, TargetTemplateID string
		Restoring                          bool
		Fields                             []struct {
			ElementID, Name string
			Required        bool
		}
	}
	if json.Unmarshal(readOffice, &office) != nil || office.ActiveTemplateID != templateID || office.TargetTemplateID != officeID || office.Restoring || len(office.Fields) != 2 ||
		office.Fields[0].ElementID != officeElement || office.Fields[0].Name != "officeProtocol" || !office.Fields[0].Required ||
		office.Fields[1].Name != "officeTag" || !office.Fields[1].Required {
		t.Fatalf("unexpected office profile: %s", readOffice)
	}
	setValue := func(action, target, protocol, value, receipt string) []byte {
		t.Helper()
		values := []map[string]string{}
		if protocol != "" {
			values = append(values, map[string]string{"elementId": officeElement, "name": "officeProtocol", "source": protocol})
		}
		if value != "" {
			values = append(values, map[string]string{"elementId": officeElement, "name": "officeTag", "source": value})
		}
		body := map[string]any{"action": action, "targetTemplateId": target, "values": values}
		if receipt != "" {
			body["receipt"] = receipt
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	request(http.MethodPost, profilePath, setValue("preview", officeID, "", "", ""), 422)
	badProfile, _ := request(http.MethodPost, profilePath, setValue("preview", officeID, `"not-a-supported-protocol"`, `"office.example"`, ""), 422)
	if !strings.Contains(string(badProfile), `"state":"catalog_rejected"`) || strings.Contains(string(badProfile), "not-a-supported-protocol") {
		t.Fatalf("unexpected validator failure: %s", badProfile)
	}
	request(http.MethodPost, profilePath, []byte(`{"action":"preview","targetTemplateId":"office / example","values":[],"receipt":null}`), 400)
	previewPayload := setValue("preview", officeID, `"blackhole"`, `"office.example"`, "")
	officePlan, _ := request(http.MethodPost, profilePath, previewPayload, 200)
	var plan struct {
		Receipt string
		Changed bool
	}
	if json.Unmarshal(officePlan, &plan) != nil || plan.Receipt == "" || !plan.Changed {
		t.Fatalf("unexpected office plan: %s", officePlan)
	}
	overview, _ := request(http.MethodGet, "/ui/v1/routers", nil, 200)
	if !strings.Contains(string(overview), `"templateId":"`+templateID+`"`) {
		t.Fatal("preparation changed active assignment")
	}
	request(http.MethodPost, profilePath, setValue("confirm", officeID, `"blackhole"`, `"changed.example"`, plan.Receipt), 409)
	officeCommit, _ := request(http.MethodPost, profilePath, setValue("confirm", officeID, `"blackhole"`, `"office.example"`, plan.Receipt), 200)
	if !strings.Contains(string(officeCommit), `"templateId":"`+officeID+`"`) || !strings.Contains(string(officeCommit), `"desiredRevision":"3"`) ||
		strings.Contains(string(officeCommit), `"appliedRevision"`) {
		t.Fatalf("invalid office result: %s", officeCommit)
	}
	request(http.MethodPost, profilePath, setValue("confirm", officeID, `"blackhole"`, `"office.example"`, plan.Receipt), 409)
	homePath := profilePath + "&targetTemplateId=" + url.QueryEscape(templateID)
	readHome, _ := request(http.MethodGet, homePath, nil, 200)
	if !strings.Contains(string(readHome), `"restoring":true`) || !strings.Contains(string(readHome), `"activeTemplateId":"`+officeID+`"`) {
		t.Fatalf("missing retained home: %s", readHome)
	}

}
