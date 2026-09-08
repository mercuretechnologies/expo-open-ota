package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"xprem/internal/services"
	"xprem/internal/store"
	"xprem/internal/validation"

	"github.com/gorilla/mux"
)

type BuildHandler struct {
	environments *services.EnvironmentService
	credentials  *services.CredentialsService
	identifiers  *services.AppIdentifierService
}

func NewBuildHandler(environments *services.EnvironmentService, credentials *services.CredentialsService, identifiers *services.AppIdentifierService) *BuildHandler {
	return &BuildHandler{environments: environments, credentials: credentials, identifiers: identifiers}
}

func RenderBuildInputError(w http.ResponseWriter, err error) {
	var missing *store.ErrResourceNotFound
	switch {
	case errors.Is(err, store.ErrBuildNumberExhausted):
		RenderError(w, http.StatusConflict, err.Error())
	case validation.IsValidationError(err):
		RenderError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &missing):
		RenderError(w, http.StatusNotFound, missing.Error())
	case errors.Is(err, store.ErrNotSupportedInStatelessMode):
		RenderError(w, http.StatusBadRequest, err.Error())
	default:
		RenderError(w, http.StatusInternalServerError, "Could not retrieve build inputs.")
	}
}

// renderBuildSecrets writes a build input export that must never be cached.
func renderBuildSecrets(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusOK, payload)
}
func (h *BuildHandler) Environment(w http.ResponseWriter, r *http.Request) {
	query, err := buildEnvironmentQuery(r)
	if err != nil {
		RenderBuildInputError(w, err)
		return
	}
	environment, err := h.environments.ExportVariables(r.Context(), mux.Vars(r)["APP_ID"], query["channel"], query["environment"])
	if err != nil {
		RenderBuildInputError(w, err)
		return
	}
	renderBuildSecrets(w, environment)
}

// Reject ambiguous selectors rather than silently using the first query value.
func buildEnvironmentQuery(r *http.Request) (map[string]string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, validation.Errorf("query", "invalid query parameters")
	}
	selectors := map[string]string{}
	for key, values := range query {
		if (key != "channel" && key != "environment") || len(values) != 1 || values[0] == "" {
			return nil, validation.Errorf("query", "expected a single non-empty channel or environment")
		}
		selectors[key] = values[0]
	}
	if len(selectors) > 1 {
		return nil, validation.Errorf("query", "channel and environment are mutually exclusive")
	}
	return selectors, nil
}

// Deliberate allowlist: never serialize the credential record or Google Play
// service account. encoding/json represents the complete file bytes as base64.
type AndroidBuildCredentials struct {
	Keystore         []byte `json:"keystore"`
	KeystorePassword string `json:"keystorePassword"`
	KeyAlias         string `json:"keyAlias"`
	KeyPassword      string `json:"keyPassword"`
}

func (h *BuildHandler) AndroidCredentials(w http.ResponseWriter, r *http.Request) {
	identifierID := services.BuildIdentifierFromContext(r.Context())
	if identifierID == "" {
		RenderCliAuthError(w, services.ErrUnauthorized)
		return
	}
	exported, err := h.credentials.ExportAndroidKeystore(r.Context(), mux.Vars(r)["APP_ID"], identifierID)
	if err != nil {
		RenderBuildInputError(w, err)
		return
	}
	renderBuildSecrets(w, AndroidBuildCredentials{Keystore: exported.Keystore, KeystorePassword: exported.KeystorePassword, KeyAlias: exported.KeyAlias, KeyPassword: exported.KeyPassword})
}

func (h *BuildHandler) AllocateBuildNumber(w http.ResponseWriter, r *http.Request) {
	identifierID := services.BuildIdentifierFromContext(r.Context())
	if identifierID == "" {
		RenderCliAuthError(w, services.ErrUnauthorized)
		return
	}
	buildNumber, err := h.identifiers.AllocateBuildNumber(r.Context(), mux.Vars(r)["APP_ID"], identifierID)
	if err != nil {
		RenderBuildInputError(w, err)
		return
	}
	RenderJSON(w, http.StatusOK, map[string]any{"buildNumber": buildNumber})
}

func (h *BuildHandler) ResolveIdentifier(w http.ResponseWriter, r *http.Request) {
	identifierID := services.BuildIdentifierFromContext(r.Context())
	if identifierID == "" {
		RenderCliAuthError(w, services.ErrUnauthorized)
		return
	}
	RenderJSON(w, http.StatusOK, map[string]string{"identifierId": identifierID})
}
