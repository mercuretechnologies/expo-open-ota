package infrastructure

import (
	"net/http"
	"xprem/ee/apikeyrestrictions"
	"xprem/internal/middleware"

	"github.com/gorilla/mux"
)

// registerBuildRoutes declares the CLI endpoints that retrieve build inputs.
func registerBuildRoutes(r *mux.Router, container *AppContainer) {
	router := r.PathPrefix("/{APP_ID}/build/{IDENTIFIER_ID}").Subrouter()
	router.Use(middleware.AppResolverMiddleware(container.AppRepo))

	build := buildGroup{
		router:       router,
		cliAuth:      container.CliAuthService,
		apiKeyAccess: container.ApiKeyAccessService,
		identifiers:   container.AppIdentifierRepo,
	}

	build.route(http.MethodGet, "/environment", container.BuildHandler.Environment,
		apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodGet, "/credentials/android", container.BuildHandler.AndroidCredentials,
		apikeyrestrictions.BuildActionCreate)
}
