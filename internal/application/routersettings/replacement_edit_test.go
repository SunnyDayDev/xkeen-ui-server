package routersettings

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestEditReplacementPreservesVariablesUntilExplicitReset(t *testing.T) {
	source := configuration.ElementSource{TemplateID: "template", ElementID: "550e8400-e29b-41d4-a716-446655440000"}
	original := configuration.ContentOverride{Source: source, Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(" 9007199254740993\r\n")}}
	base := Draft{ID: "router", TemplateID: "template", Groups: []Group{{
		Name: "rules", Overrides: []configuration.ContentOverride{original}, Exclusions: []configuration.ElementSource{source},
	}}, PersonalOrder: []string{source.ElementID}, DisabledRules: []string{source.ElementID}}
	raw := json.RawMessage(" \r\n{\"literal\":\"$" + "{port}\",\"null\":null}\n")
	next, err := EditReplacement(base, "rules", source, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: raw})
	if err != nil {
		t.Fatal(err)
	}
	saved := next.Groups[0].Overrides[0]
	if saved.Source != source || saved.Mode != configuration.ContentModeReplacement || string(saved.Content) != string(raw) ||
		!reflect.DeepEqual(saved.Values, original.Values) || !reflect.DeepEqual(next.Groups[0].Exclusions, base.Groups[0].Exclusions) {
		t.Fatal("replacement lost exact source, identity, retained variables or exclusion")
	}
	reset, err := EditReplacement(next, "rules", source, configuration.ContentAction{Kind: configuration.ResetContentOverride})
	if err != nil || len(reset.Groups[0].Overrides) != 0 || !reflect.DeepEqual(reset.Groups[0].Exclusions, base.Groups[0].Exclusions) ||
		!reflect.DeepEqual(reset.PersonalOrder, base.PersonalOrder) || !reflect.DeepEqual(reset.DisabledRules, base.DisabledRules) {
		t.Fatalf("full reset affected unrelated ordering or exclusion: %v", err)
	}
	inherited, err := EditReplacement(next, "rules", source, configuration.ContentAction{Kind: configuration.ReturnToVariables})
	if err != nil || inherited.Groups[0].Overrides[0].Mode != configuration.ContentModeVariables ||
		inherited.Groups[0].Overrides[0].Content != nil || !reflect.DeepEqual(inherited.Groups[0].Overrides[0].Values, original.Values) {
		t.Fatalf("return did not restore inactive values: %v", err)
	}
	if base.Groups[0].Overrides[0].Mode != configuration.ContentModeVariables || next.Groups[0].Overrides[0].Mode != configuration.ContentModeReplacement {
		t.Fatal("an edit modified its input")
	}
	asNull, err := EditReplacement(base, "rules", source, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage("null")})
	if err != nil || string(asNull.Groups[0].Overrides[0].Content) != "null" {
		t.Fatal("valid root null is literal")
	}
	var invalid *ReplacementSourceError
	if _, err := EditReplacement(base, "rules", source, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage(`{"x":0,"x":1}`)}); !errors.As(err, &invalid) {
		t.Fatalf("duplicate key did not yield scoped source error: %v", err)
	}
	if _, err := EditReplacement(base, "outbounds", source, configuration.ContentAction{Kind: configuration.ResetContentOverride}); !errors.Is(err, ErrSharedScope) {
		t.Fatalf("foreign section accepted: %v", err)
	}
}
