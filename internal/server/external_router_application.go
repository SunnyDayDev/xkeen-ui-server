package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type externalRouterApplicationStore interface {
	ReadExternalRouterApplication(context.Context, externalaccess.Permit, string, int64) (reporting.Status, error)
}

func NewExternalRouterApplication(store externalRouterApplicationStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/application" {
			next(w, r, permit)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids, revisions := query["routerId"], query["revision"]
		if err != nil || len(query) != 2 || len(ids) != 1 || ids[0] == "" ||
			!utf8.ValidString(ids[0]) || len(revisions) != 1 {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		number, err := strconv.ParseInt(revisions[0], 10, 64)
		if err != nil || number <= 0 || strconv.FormatInt(number, 10) != revisions[0] {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		status, err := store.ReadExternalRouterApplication(r.Context(), permit, ids[0], number)
		if errors.Is(err, reporting.ErrUnknownRevision) {
			externalError(w, http.StatusNotFound, "not_found")
			return
		}
		if err != nil {
			externalFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(externalApplicationEvidence(ids[0], number, status))
	}
}

type applicationEvidence struct {
	RouterID            string             `json:"routerId"`
	Revision            string             `json:"revision"`
	LatestDesired       string             `json:"latestDesiredRevision"`
	HistoricallyApplied string             `json:"historicallyAppliedRevision,omitempty"`
	Contact             reporting.Presence `json:"contact"`
	Runtime             string             `json:"runtime,omitempty"`
	Result              string             `json:"result"`
	Stage               string             `json:"stage,omitempty"`
	Code                string             `json:"code,omitempty"`
}

func externalApplicationEvidence(id string, number int64, status reporting.Status) applicationEvidence {
	result := applicationEvidence{RouterID: id, Revision: strconv.FormatInt(number, 10),
		LatestDesired: strconv.FormatInt(status.Desired, 10), HistoricallyApplied: optionalWebRevision(status.Applied),
		Contact: status.Presence, Runtime: status.Runtime, Result: "unknown"}
	switch {
	case status.Last != nil && status.Last.Revision == number && status.Last.State == "failed":
		result.Result, result.Stage, result.Code = "failed", status.Last.Stage, status.Last.Code
	case status.Last != nil && status.Last.Revision == number && status.Last.State == "applying":
		result.Result = "applying"
	case status.Applied == number && status.Runtime == "verified":
		result.Result = "applied"
	}
	return result
}
