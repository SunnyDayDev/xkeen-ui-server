package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

type TemplateGroupPreview struct {
	TemplateID               string
	Version, AcceptedVersion int64
	Members                  []GroupMemberPreview
	Fingerprint              [sha256.Size]byte
}

type GroupMemberPreview struct {
	Expected GroupRouterExpectation
	Name     string
	Changes  []CatalogFileChange
	Estimate string
}

type CatalogFileChange struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Preview validates the complete saved group using the same preparation as
// Publish. It never writes a publication or signals a member.
func (s *TemplateGroupService) Preview(ctx context.Context, id string, version int64) (*TemplateGroupPreview, error) {
	plan, views, err := s.prepare(ctx, SharedPublishRequest{
		TemplateID: id, TemplateDraftVersion: version, Source: SourceSharedDraft,
	}, true)
	if err != nil {
		return nil, err
	}
	return &TemplateGroupPreview{
		TemplateID: id, Version: plan.TemplateDraftVersion, AcceptedVersion: plan.AcceptedVersion,
		Members: views, Fingerprint: fingerprintGroupPlan(plan),
	}, nil
}

func previewGroupMember(member TemplateGroupMember, plan GroupRouterPlan) GroupMemberPreview {
	name := member.Active.Draft.Name
	if name == "" {
		name = plan.Expected.RouterID
	}
	view := GroupMemberPreview{Expected: plan.Expected, Name: name, Estimate: "unchanged", Changes: []CatalogFileChange{}}
	if plan.NewRevision != nil {
		view.Changes, view.Estimate = catalogFileChanges(member.Desired.Catalog.Files, plan.NewRevision.Files)
	}
	return view
}

func catalogFileChanges(before, after []configuration.CatalogFile) ([]CatalogFileChange, string) {
	current := make(map[string][]byte, len(before))
	for _, file := range before {
		current[file.Name] = file.Content
	}
	changes := make([]CatalogFileChange, 0)
	var rulesOnly bool
	for _, file := range after {
		previous, exists := current[file.Name]
		switch {
		case !exists:
			changes = append(changes, CatalogFileChange{Name: file.Name, Kind: "added"})
		case !bytes.Equal(previous, file.Content):
			changes = append(changes, CatalogFileChange{Name: file.Name, Kind: "changed"})
			if file.Name == "05_routing.json" {
				rulesOnly = routingRulesOnly(previous, file.Content)
			}
		}
		delete(current, file.Name)
	}
	for name := range current {
		changes = append(changes, CatalogFileChange{Name: name, Kind: "removed"})
	}
	slices.SortFunc(changes, func(a, b CatalogFileChange) int { return strings.Compare(a.Name, b.Name) })
	switch {
	case len(changes) == 0:
		return changes, "unchanged"
	case len(changes) == 1 && rulesOnly:
		return changes, "hot_rules_possible"
	default:
		return changes, "restart_expected"
	}
}

func routingRulesOnly(previous, next []byte) bool {
	before, oldRules, oldOK := splitRoutingRules(previous)
	after, newRules, newOK := splitRoutingRules(next)
	return oldOK && newOK && !reflect.DeepEqual(oldRules, newRules) && reflect.DeepEqual(before, after)
}

func splitRoutingRules(raw []byte) (map[string]any, []any, bool) {
	value, issue := jsonvalue.Parse(raw)
	obj, _ := value.(map[string]any)
	routing, _ := obj["routing"].(map[string]any)
	rules, ok := routing["rules"].([]any)
	if issue != nil || !ok {
		return nil, nil, false
	}
	delete(routing, "rules")
	return obj, rules, true
}

type publicationFileFingerprint struct {
	Name   string
	Digest [sha256.Size]byte
}

// Members arrive in stable RouterID order from prepare. Hash files separately
// so attesting a group does not serialize another copy of each full catalog.
func fingerprintGroupPlan(plan TemplateGroupPlan) [sha256.Size]byte {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode([]any{plan.TemplateID, plan.TemplateDraftVersion, plan.AcceptedVersion})
	for _, member := range plan.Members {
		var profile string
		var files []publicationFileFingerprint
		if member.NewRevision != nil {
			profile = member.NewRevision.Profile
			files = make([]publicationFileFingerprint, 0, len(member.NewRevision.Files))
			for _, file := range member.NewRevision.Files {
				files = append(files, publicationFileFingerprint{Name: file.Name, Digest: sha256.Sum256(file.Content)})
			}
			slices.SortFunc(files, func(a, b publicationFileFingerprint) int { return strings.Compare(a.Name, b.Name) })
		}
		_ = encoder.Encode([]any{member.Expected, member.PersonalOrder, member.OrderBasis, profile, files})
	}
	return [sha256.Size]byte(hash.Sum(nil))
}

type TemplateGroupCurrent struct {
	TemplateID               string
	Version, AcceptedVersion int64
	Routers                  []TemplateGroupCurrentRouter
}

type TemplateGroupCurrentRouter struct {
	RouterID string
	Name     string
	Desired  int64
}

// ReadTemplateGroupCurrent reads existing numbers without attributing them to a particular confirmation.
func ReadTemplateGroupCurrent(ctx context.Context, store TemplateGroupStore, id string) (*TemplateGroupCurrent, error) {
	snapshot, err := store.ReadTemplateGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.TemplateDraft == nil || snapshot.TemplateDraft.ID != id {
		return nil, ErrStoredState
	}
	current := &TemplateGroupCurrent{TemplateID: id, Version: snapshot.TemplateDraft.Version,
		Routers: make([]TemplateGroupCurrentRouter, 0, len(snapshot.Members))}
	if snapshot.Accepted != nil {
		current.AcceptedVersion = snapshot.Accepted.Version
	}
	for _, member := range snapshot.Members {
		name := member.Active.Draft.Name
		if name == "" {
			name = member.Active.Draft.ID
		}
		current.Routers = append(current.Routers, TemplateGroupCurrentRouter{
			RouterID: member.Active.Draft.ID, Name: name, Desired: member.Desired.Number,
		})
	}
	return current, nil
}
