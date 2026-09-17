package configuration_test

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"reflect"
	"testing"
)

func TestReferenceDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		names         []string
	}{
		{"defaults", `{"port":"${port}","options":"${options}"}`, []string{"options", "port"}},
		{"root", `"${host}"`, []string{"host"}},
		{"nested", `{"a":"${host}","nested":["${host}",{"b":"${host}"}]}`, []string{"host"}},
		{"exact", `["${host}","${Host}","${server.address}","${сервер}","${café}","${café}"]`, []string{"Host", "café", "café", "host", "server.address", "сервер"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := configuration.Element{ID: elementUUID, Content: tc.content}
			got, issues := configuration.PrepareElement(input, elementLocation, nil)
			if got == nil || len(issues) != 0 {
				t.Fatalf("prepare: %+v, %+v", got, issues)
			}
			var names []string
			for _, v := range got.Variables {
				names = append(names, v.Name)
				if v.Type != "string" || string(v.Default) != `""` {
					t.Fatalf("bad default: %+v", v)
				}
			}
			if !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("names: %v; want %v", names, tc.names)
			}
			if got.Content != input.Content || len(input.Variables) != 0 {
				t.Fatal("changed input or source")
			}
		})
	}
}

func TestDeclarationSyntax(t *testing.T) {
	for _, tc := range []struct {
		content string
		count   int
	}{
		{`{"value":"\u0024{host}"}`, 1},
		{`"$${host}"`, 0}, {`"$$${host}"`, 0}, {`"https://${host}/path"`, 0}, {`" ${host} "`, 0},
		{`"${host}${port}"`, 0}, {`"${}"`, 0}, {`"${host"`, 0}, {`"${server address}"`, 0},
		{`"${server\u00a0address}"`, 0}, {`"${server\u0001address}"`, 0}, {`"${a{b}"`, 0},
		{`{"${key}":"fixed"}`, 0}, {`"\\${host}"`, 0},
	} {
		t.Run(tc.content, func(t *testing.T) {
			got, issues := configuration.PrepareElement(configuration.Element{ID: elementUUID, Content: tc.content}, elementLocation, nil)
			if got == nil || len(issues) != 0 || len(got.Variables) != tc.count || got.Content != tc.content {
				t.Fatalf("got %+v, %+v; want %d definitions", got, issues, tc.count)
			}
		})
	}
	in := configuration.Element{ID: elementUUID, Content: `{}`, Variables: []configuration.VariableDefinition{{Name: "data", Type: "object", Default: []byte(`{"label":"${other}"}`)}}}
	got, issues := configuration.PrepareElement(in, elementLocation, nil)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Variables, in.Variables) {
		t.Fatalf("scanned default: %+v, %+v", got, issues)
	}
}

func TestExistingDefinitionsAndRepeatedSave(t *testing.T) {
	for _, def := range []string{`443`, `""`} {
		t.Run(def, func(t *testing.T) {
			in := configuration.Element{ID: elementUUID, Content: `{"port":"${port}"}`, Variables: []configuration.VariableDefinition{{Name: "port", Type: "number", Default: []byte(def)}}}
			for i := 0; i < 2; i++ {
				got, issues := configuration.PrepareElement(in, elementLocation, nil)
				if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Variables, in.Variables) {
					t.Fatalf("settings changed: %+v, %+v", got, issues)
				}
				in = *got
			}
		})
	}
}

func TestReferenceEditLifecycle(t *testing.T) {
	existing := []configuration.VariableDefinition{{Name: "port", Type: "number", Default: []byte(`443`)}, {Name: "host", Type: "string", Default: []byte(`""`)}}
	for _, tc := range []struct {
		name, content string
		vars          []configuration.VariableDefinition
		want          []string
	}{
		{"removed", `{}`, existing, []string{"port", "host"}},
		{"restored", `"${port}"`, existing, []string{"port", "host"}},
		{"deleted", `{}`, nil, nil},
		{"redeclared", `"${host}"`, nil, []string{"host"}},
		{"renamed", `"${server.address}"`, existing, []string{"port", "host", "server.address"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := configuration.Element{ID: elementUUID, Content: tc.content, Variables: tc.vars}
			got, issues := configuration.PrepareElement(in, elementLocation, nil)
			if got == nil || len(issues) != 0 {
				t.Fatalf("prepare: %+v %+v", got, issues)
			}
			var names []string
			for _, v := range got.Variables {
				names = append(names, v.Name)
			}
			if !reflect.DeepEqual(names, tc.want) {
				t.Fatalf("names %v; want %v", names, tc.want)
			}
			for i, v := range tc.vars {
				if !reflect.DeepEqual(got.Variables[i], v) {
					t.Fatal("lost existing settings")
				}
			}
			for _, v := range got.Variables[len(tc.vars):] {
				if v.Type != "string" || string(v.Default) != `""` {
					t.Fatal("wrong new defaults")
				}
			}
		})
	}
}

func TestRestoreDoesNotDiscoverOrRepair(t *testing.T) {
	in := configuration.Element{ID: elementUUID, Content: `"${host}"`, Variables: []configuration.VariableDefinition{
		{Name: "bad name", Type: "integer", Default: []byte(`null`)},
		{Name: "duplicate", Type: "number", Default: []byte(`"443"`)},
		{Name: "duplicate", Type: "object", Default: []byte(`[]`)},
	}}
	read, issues := configuration.RestoreElement(in, elementLocation)
	if read == nil || len(issues) != 0 || !reflect.DeepEqual(*read, in) {
		t.Fatalf("restore repaired: %+v %+v", read, issues)
	}
	saved, issues := configuration.PrepareElement(*read, elementLocation, nil)
	if saved == nil || len(issues) != 0 || len(saved.Variables) != 4 || saved.Variables[3].Name != "host" {
		t.Fatalf("prepare: %+v %+v", saved, issues)
	}
	if !reflect.DeepEqual(saved.Variables[:3], in.Variables) {
		t.Fatal("repaired existing definitions")
	}
}
