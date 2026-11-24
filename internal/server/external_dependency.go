package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

type externalDependency struct {
	RuleID    string   `json:"ruleId"`
	ElementID string   `json:"elementId,omitempty"`
	Kind      string   `json:"kind"`
	Path      string   `json:"path"`
	Status    string   `json:"status"`
	Targets   []string `json:"targets"`
	PartIndex int      `json:"partIndex"`
}

type externalRouterDraftReader interface {
	ReadExternalRouterDraft(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error)
}

func NewExternalRouterDependencies(store externalRouterDraftReader, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/dependencies" {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			externalError(w, 405, "method_not_allowed")
			return
		}
		id, element, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || element != "" {
			externalError(w, 400, "invalid_request")
			return
		}
		snap, err := store.ReadExternalRouterDraft(r.Context(), permit, id)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		analysis, analysisErr := publication.AnalyzeDraftDependencies(snap.Accepted, snap.Editable, snap.OrderBasis)
		current, err := store.ReadExternalRouterDraft(r.Context(), permit, id)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		if current.Basis != snap.Basis {
			externalError(w, 409, "snapshot_changed")
			return
		}
		var invalid *publication.PreparationError
		if errors.As(analysisErr, &invalid) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			_ = json.NewEncoder(w).Encode(struct {
				Code   string                    `json:"code"`
				Issues []routerCatalogDiagnostic `json:"issues"`
			}{"analysis_unavailable", routerCatalogDiagnostics(invalid)})
			return
		}
		if errors.Is(analysisErr, publication.ErrSourceMismatch) {
			externalRouterFailure(w, routerdraft.ErrForeign)
			return
		}
		if analysisErr != nil {
			externalRouterFailure(w, analysisErr)
			return
		}
		refs := make([]externalDependency, 0, len(analysis.Inbound.References)+len(analysis.Outbound.References))
		for _, ref := range analysis.Inbound.References {
			ids := []string{}
			if ref.InboundID != "" {
				ids = append(ids, ref.InboundID)
			}
			refs = append(refs, externalDependency{RuleID: ref.RuleID, Kind: "inbound", Path: ref.Path, Status: ref.Status, Targets: ids, PartIndex: ref.PartIndex})
		}
		for _, ref := range analysis.Outbound.References {
			ids := append([]string{}, ref.OutboundIDs...)
			refs = append(refs, externalDependency{RuleID: ref.RuleID, ElementID: ref.ElementID, Kind: ref.Kind, Path: ref.Path, Status: ref.Status, Targets: ids, PartIndex: ref.PartIndex})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			externalRouterBasis
			Ready      bool                 `json:"ready"`
			References []externalDependency `json:"references"`
		}{externalRouterBasisFrom(snap.Basis), analysis.Inbound.Ready && analysis.Outbound.Ready, refs})
	}
}
