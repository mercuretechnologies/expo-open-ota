package infrastructure

import (
	"context"
	"net/http"
	"xprem/ee/apikeyrestrictions"
	"xprem/internal/handlers"
	"xprem/internal/helpers"
	"xprem/internal/services"
	"xprem/internal/store"
	"xprem/internal/validation"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type buildAccessPolicy interface {
	AuthorizeBuild(context.Context, apikeyrestrictions.BuildRequest) error
}
type buildGroup struct {
	router       *mux.Router
	cliAuth      *services.CliAuthService
	apiKeyAccess buildAccessPolicy
	identifiers  services.AppIdentifierRepository
}

// route applies the action declared by each build-input endpoint.
func (g buildGroup) route(method, path string, handler http.HandlerFunc, action apikeyrestrictions.BuildAction) {
	g.router.Handle(path, g.guard(action)(handler)).Methods(method)
}

func (g buildGroup) guard(action apikeyrestrictions.BuildAction) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			credential, err := g.cliAuth.AuthenticateCliCredential(r.Context(), vars["APP_ID"], helpers.GetAuth(r))
			if err != nil {
				handlers.RenderCliAuthError(w, err)
				return
			}
			// Build requires a registered app token; Expo stateless credentials have no key ID.
			if credential.KeyID <= 0 {
				handlers.RenderCliAuthError(w, services.ErrUnauthorized)
				return
			}
			if g.identifiers == nil {
				handlers.RenderBuildInputError(w, store.ErrNotSupportedInStatelessMode)
				return
			}
			identifierID, err := uuid.Parse(vars["IDENTIFIER_ID"])
			if err != nil {
				handlers.RenderBuildInputError(w, validation.Errorf("identifierId", "invalid identifier id"))
				return
			}
			ref, err := g.identifiers.GetAppIdentifierByID(r.Context(), credential.AppID, identifierID.String())
			if err != nil {
				handlers.RenderBuildInputError(w, err)
				return
			}
			if ref == nil {
				handlers.RenderBuildInputError(w, &store.ErrResourceNotFound{Resource: "app identifier", Identifier: identifierID.String()})
				return
			}
			err = g.apiKeyAccess.AuthorizeBuild(r.Context(), apikeyrestrictions.BuildRequest{
				APIKeyContext:   apikeyrestrictions.APIKeyContext{AppID: credential.AppID, APIKeyID: credential.KeyID, ClientIP: helpers.ClientIP(r)},
				AppIdentifierID: ref.Id,
				Action:          action,
			})
			if err != nil {
				handlers.RenderCliAuthError(w, err)
				return
			}
			// Both exports use the resolved canonical ID, never a payload-selected target.
			vars["IDENTIFIER_ID"] = ref.Id
			next.ServeHTTP(w, r.WithContext(services.WithCliAuth(r.Context(), credential)))
		})
	}
}
