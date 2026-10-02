package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	c "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"os"
	"path/filepath"
)

func buildDemoCatalog(invalidProtocol bool) (*c.XrayCatalog, []c.CatalogIssue) {
	template, router := c.Owner{Kind: "template", ID: "demo-template"}, c.Owner{Kind: "router", ID: "demo-router"}
	group := func() *c.RouterSelectionInput { return &c.RouterSelectionInput{Template: template, Router: router} }
	id := func(n int) string { return fmt.Sprintf("550e8400-e29b-41d4-a716-%012d", n) }
	shared := func(g *c.RouterSelectionInput, n int, position, raw string) {
		g.Shared = append(g.Shared, c.SharedElement{Element: c.Element{ID: id(n), Content: raw}, Position: position})
	}
	local := func(g *c.RouterSelectionInput, n int, position, raw string) {
		g.Local = append(g.Local, c.LocalContent{ID: id(n), Content: json.RawMessage(raw), Position: position})
	}
	in := c.CatalogInput{Template: template, Router: router, Log: group(), API: group(), Inbounds: group(), Outbounds: group(), Routing: group(), Rules: group(), Stats: group()}
	shared(in.Log, 1, "/log/0", `{"loglevel":"warning"}`)
	shared(in.API, 2, "/api/0", `{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService","StatsService"]}`)
	shared(in.Stats, 3, "/stats/0", `{}`)
	shared(in.Inbounds, 10, "/inbounds/0", `{"tag":"demo-socks","listen":"127.0.0.1","port":"${port}","protocol":"socks","settings":{"auth":"noauth","udp":false}}`)
	in.Inbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`1080`)}}
	in.Inbounds.Overrides = []c.LocatedContentOverride{{Position: "/values/inbound", Override: c.ContentOverride{Source: c.ElementSource{TemplateID: template.ID, ElementID: id(10)}, Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`1081`)}}}}
	shared(in.Outbounds, 11, "/outbounds/0", `{"tag":"direct","protocol":"${protocol}","extension":{"values":[9007199254740993,false,{"mode":"custom"}]}}`)
	protocol := `"freedom"`
	if invalidProtocol {
		protocol = `"invalid-demo-protocol"`
	}
	in.Outbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "protocol", Type: "string", Default: json.RawMessage(protocol)}}
	shared(in.Outbounds, 12, "/outbounds/1", `{"tag":"old","protocol":"freedom"}`)
	in.Outbounds.Overrides = []c.LocatedContentOverride{{Position: "/replacement/outbound", Override: c.ContentOverride{Source: c.ElementSource{TemplateID: template.ID, ElementID: id(12)}, Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"tag":"block","protocol":"blackhole","extension":{"literal":"${literal}"}}`)}}}
	shared(in.Outbounds, 13, "/outbounds/2", `{"tag":"${unfinished}"}`)
	in.Outbounds.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: template.ID, ElementID: id(13)}, Position: "/exclusions/outbound"}}
	shared(in.Rules, 20, "/rules/0", `{"domain":"${unfinished}"}`)
	shared(in.Rules, 21, "/rules/1", `{"outboundTag":"missing"}`)
	shared(in.Rules, 22, "/rules/2", `{"type":"field","ruleTag":"demo-shared","inboundTag":["demo-socks"],"domain":["domain:example.net"],"outboundTag":"direct"}`)
	local(in.Rules, 23, "/local/rules/0", `{"type":"field","ruleTag":"demo-local","inboundTag":["demo-socks"],"domain":["domain:blocked.example.net"],"outboundTag":"block"}`)
	in.TemplateOrder = []string{id(20), id(21), id(22)}
	in.PersonalOrder = []string{id(23), id(20), id(22), id(21)}
	in.DisabledRules = []c.ElementRef{{ID: id(20), Position: "/disabled/rules/0"}}
	in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: template.ID, ElementID: id(21)}, Position: "/exclusions/rule"}}
	shared(in.Routing, 30, "/routing/0", `{"domainStrategy":"AsIs","extension":{"flag":true}}`)
	dns, future := group(), group()
	local(dns, 31, "/dns/0", `{"servers":["192.0.2.53"]}`)
	local(future, 32, "/future/0", `[9007199254740993,false,null]`)
	in.Extra = []c.CatalogSectionInput{{Name: "dns", Selection: *dns}, {Name: "futureSection", Selection: *future}}
	return c.AssembleXrayCatalog(in)
}

// writeCatalog never mixes a candidate with previous files and removes only files
// it created if a write fails. It is a demonstration adapter, not publication.
func writeCatalog(dir string, catalog *c.XrayCatalog) (err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("output directory must be empty")
	}
	if catalog == nil {
		return errors.New("missing candidate")
	}
	created := []string{}
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for _, file := range catalog.Files {
		if filepath.Base(file.Name) != file.Name || filepath.Ext(file.Name) != ".json" {
			return errors.New("invalid candidate filename")
		}
		path := filepath.Join(dir, file.Name)
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return openErr
		}
		created = append(created, path)
		_, writeErr := f.Write(file.Content)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("xkeen-config-demo", flag.ContinueOnError)
	dir := flags.String("dir", "", "existing empty output directory")
	invalid := flags.Bool("invalid-protocol", false, "generate a candidate rejected by Xray")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return errors.New("usage: xkeen-config-demo -dir <empty-directory> [-invalid-protocol]")
	}
	catalog, issues := buildDemoCatalog(*invalid)
	if len(issues) != 0 {
		return fmt.Errorf("demo assembly failed: %s", issues[0].Code)
	}
	return writeCatalog(*dir, catalog)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
