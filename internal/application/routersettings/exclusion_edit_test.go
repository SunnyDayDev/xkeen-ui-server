package routersettings

import (
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestExclusionReturnAndReset(t *testing.T) {
	source := configuration.ElementSource{TemplateID: "example", ElementID: "12345678-1234-4234-8234-123456789abc"}
	initial := Draft{ID: "first", TemplateID: "example"}
	values := map[string]string{"address": "192.0.2.5"}
	withValues, err := EditExclusion(initial, source, ExclusionEdit{Action: "values", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	values["address"] = "192.0.2.99"
	blank := ""
	replaced, err := EditExclusion(withValues, source, ExclusionEdit{Action: "replace", Text: &blank})
	if err != nil || replaced.Exclusion.Replacement == nil || *replaced.Exclusion.Replacement != "" {
		t.Fatal(replaced, err)
	}
	restored, err := EditExclusion(replaced, source, ExclusionEdit{Action: "return"})
	if err != nil || restored.Exclusion.Replacement != nil || restored.Exclusion.Values["address"] != "192.0.2.5" {
		t.Fatal(restored, err)
	}
	reset, err := EditExclusion(restored, source, ExclusionEdit{Action: "reset"})
	if err != nil || reset.Exclusion != nil || restored.Exclusion == nil || initial.Exclusion != nil {
		t.Fatal(reset, err)
	}
	foreign := source
	foreign.TemplateID = "other"
	if _, err := EditExclusion(replaced, foreign, ExclusionEdit{Action: "reset"}); err == nil {
		t.Fatal("foreign reset")
	}
}
