package infrastructure

import (
	"net/http"
	"xprem/ee/apikeyrestrictions"
	"xprem/internal/middleware"

	"github.com/gorilla/mux"
)

// registerBuildRoutes declares the CLI build endpoints: inputs, artifact lifecycle and the public upload and share links.
func registerBuildRoutes(r *mux.Router, container *AppContainer) {
	router := r.PathPrefix("/{APP_ID}/build").Subrouter()
	router.Use(middleware.AppResolverMiddleware(container.AppRepo))

	build := buildGroup{
		router:       router,
		cliAuth:      container.CliAuthService,
		apiKeyAccess: container.ApiKeyAccessService,
		identifiers:  container.AppIdentifierRepo,
	}

	build.route(http.MethodPut, "/{IDENTIFIER_ID}/artifacts/{BUILD_ID}/start", container.BuildRegistryHandler.Start, apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodPut, "/{IDENTIFIER_ID}/artifacts/{BUILD_ID}", container.BuildRegistryHandler.RegisterArtifact, apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodPost, "/{IDENTIFIER_ID}/artifacts/{BUILD_ID}/failed", container.BuildRegistryHandler.Fail, apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodPost, "/{IDENTIFIER_ID}/artifacts/{BUILD_ID}/complete", container.BuildRegistryHandler.Complete, apikeyrestrictions.BuildActionCreate)
	r.HandleFunc("/build-uploads/{TOKEN}", container.BuildRegistryHandler.UploadLocal).Methods(http.MethodPut)

	build.route(http.MethodGet, "/resolve/{PLATFORM}/{APPLICATION_ID}", container.BuildHandler.ResolveIdentifier,
		apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodPost, "/{IDENTIFIER_ID}/build-number", container.BuildHandler.AllocateBuildNumber,
		apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodGet, "/{IDENTIFIER_ID}/environment", container.BuildHandler.Environment,
		apikeyrestrictions.BuildActionCreate)
	build.route(http.MethodGet, "/{IDENTIFIER_ID}/credentials/android", container.BuildHandler.AndroidCredentials,
		apikeyrestrictions.BuildActionCreate)
}
