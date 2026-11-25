package publication

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func ownProfileVariableEdits(edits []ProfileVariableEdit) []ProfileVariableEdit {
	owned := append([]ProfileVariableEdit{}, edits...)
	for i := range owned {
		if owned[i].Source != nil {
			source := *owned[i].Source
			owned[i].Source = &source
		}
	}
	slices.SortFunc(owned, func(a, b ProfileVariableEdit) int {
		if cmp := strings.Compare(a.ElementID, b.ElementID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	return owned
}

func editProfileVariables(template *templates.Snapshot, draft routersettings.Draft, edits []ProfileVariableEdit) (routersettings.Draft, error) {
	if len(edits) == 0 {
		return draft, nil
	}
	type destination struct {
		section string
		element configuration.TemplateElement
	}
	sources := make(map[string]destination)
	for _, section := range template.Sections {
		for _, element := range section.Elements {
			sources[element.Element.ID] = destination{section.Name, element}
		}
	}
	for i := 0; i < len(edits); {
		id := edits[i].ElementID
		selected, found := sources[id]
		if !found {
			return routersettings.Draft{}, ErrInvalidCandidate
		}
		changes := []routersettings.VariableChange{}
		for ; i < len(edits) && edits[i].ElementID == id; i++ {
			edit := edits[i]
			change := routersettings.VariableChange{Name: edit.Name}
			if edit.Source != nil {
				change.Value = json.RawMessage(*edit.Source)
			}
			changes = append(changes, change)
		}
		source := configuration.ElementSource{TemplateID: template.ID, ElementID: id}
		next, err := routersettings.EditVariableValues(draft, selected.section, source, selected.element.Element.Variables, changes)
		if err != nil {
			return routersettings.Draft{}, fmt.Errorf("%w: %w", ErrInvalidCandidate, err)
		}
		draft = next
	}
	return draft, nil
}

func fingerprintProfileCandidate(basis ProfileSwitchBasis, edits []ProfileVariableEdit, prepared *PreparedRouterCandidate) [sha256.Size]byte {
	files := make([]publicationFileFingerprint, 0, len(prepared.Catalog.Files))
	for _, file := range prepared.Catalog.Files {
		files = append(files, publicationFileFingerprint{Name: file.Name, Digest: sha256.Sum256(file.Content)})
	}
	slices.SortFunc(files, func(a, b publicationFileFingerprint) int { return strings.Compare(a.Name, b.Name) })
	digest := sha256.New()
	_ = json.NewEncoder(digest).Encode([]any{
		"router-profile-switch-v1", basis, edits, prepared.PersonalOrder, prepared.OrderBasis, configuration.XrayIdentityVersion, files,
	})
	return [sha256.Size]byte(digest.Sum(nil))
}
