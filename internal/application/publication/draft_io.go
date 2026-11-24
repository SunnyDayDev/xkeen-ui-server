package publication

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func SelectDraftIO(template *templates.Snapshot, draft routersettings.Draft) (inbounds, outbounds []configuration.CatalogIOMember, err error) {
	if template == nil || template.ID != draft.TemplateID || template.Version < 1 {
		return nil, nil, ErrSourceMismatch
	}
	input, issues := prepareCatalogInput(template, &draft)
	if len(issues) != 0 {
		return nil, nil, &PreparationError{Issues: issues}
	}
	inbounds, outbounds, issues = configuration.SelectCatalogIO(input)
	if len(issues) != 0 {
		return nil, nil, &PreparationError{Issues: issues}
	}
	return inbounds, outbounds, nil
}
