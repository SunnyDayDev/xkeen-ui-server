package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webRouterCatalogService interface {
	Read(context.Context, string, string, publication.RouterPreviewMode) (*publication.RouterCatalogPreview, error)
}

func NewWebRouterCatalogHandler(origin WebOrigin, service webRouterCatalogService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/catalog" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet) {
			return
		}
		id, mode, ok := routerCatalogQuery(r.URL.RawQuery)
		if !ok {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		result, err := service.Read(r.Context(), webSessionToken(r), id, mode)
		if err != nil {
			webRouterCatalogFailure(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID        string              `json:"routerId"`
			Source          string              `json:"source"`
			AcceptedVersion string              `json:"acceptedVersion"`
			DesiredRevision string              `json:"desiredRevision"`
			Files           []routerCatalogFile `json:"files"`
		}{result.Expected.RouterID, string(result.Mode), strconv.FormatInt(result.Expected.AcceptedVersion, 10),
			strconv.FormatInt(result.Expected.DesiredNumber, 10), routerCatalogFiles(result)})
	})
}

func routerCatalogQuery(raw string) (string, publication.RouterPreviewMode, bool) {
	query, err := url.ParseQuery(raw)
	ids, modes := query["routerId"], query["source"]
	if err != nil || len(query) != 2 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) || len(modes) != 1 {
		return "", "", false
	}
	mode := publication.RouterPreviewMode(modes[0])
	return ids[0], mode, mode == publication.PreviewDesired || mode == publication.PreviewPersonal
}

type routerCatalogFile struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

func routerCatalogFiles(result *publication.RouterCatalogPreview) []routerCatalogFile {
	files := make([]routerCatalogFile, 0, len(result.Files))
	for _, current := range result.Files {
		files = append(files, routerCatalogFile{Name: current.Name, Content: current.Content})
	}
	return files
}

type routerCatalogDiagnostic struct {
	Code     string `json:"code"`
	Position string `json:"position"`
}

func routerCatalogDiagnostics(prepared *publication.PreparationError) []routerCatalogDiagnostic {
	issues := make([]routerCatalogDiagnostic, 0, len(prepared.Issues))
	for _, issue := range prepared.Issues {
		issues = append(issues, routerCatalogDiagnostic{Code: issue.Code, Position: issue.Location.Position})
	}
	return issues
}

func webRouterCatalogFailure(w http.ResponseWriter, err error) {
	var prepared *publication.PreparationError
	switch {
	case errors.As(err, &prepared):

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State  string                    `json:"state"`
			Issues []routerCatalogDiagnostic `json:"issues"`
		}{"catalog_unready", routerCatalogDiagnostics(prepared)})
	case errors.Is(err, publication.ErrInvalidCandidate):
		webResult(w, http.StatusUnprocessableEntity, "catalog_unready", "")
	case errors.Is(err, publication.ErrConflict):
		webResult(w, http.StatusConflict, "catalog_changed", "")
	case errors.Is(err, publication.ErrSourceMismatch):
		webResult(w, http.StatusConflict, "different_template", "")
	case errors.Is(err, publication.ErrSourceMissing), errors.Is(err, webadmin.ErrRouterDraftMissing):
		webResult(w, http.StatusNotFound, "router_unavailable", "")
	default:
		webFailure(w, err)
	}
}
