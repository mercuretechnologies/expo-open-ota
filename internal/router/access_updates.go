package infrastructure

import (
	"context"
	"net/http"
	"strings"
	"xprem/ee/apikeyrestrictions"
	"xprem/internal/bucket"
	"xprem/internal/handlers"
	"xprem/internal/helpers"
	"xprem/internal/services"

	"github.com/gorilla/mux"
)

// updateAccessPolicy is the Updates authorization contract used by OTA routes.
type updateAccessPolicy interface {
	AuthorizeUpdates(ctx context.Context, req apikeyrestrictions.UpdateRequest) error
}

// authorizeUpdateRequest checks the Updates permission for an authenticated token,
// writing the response and returning false when the request is refused.
func authorizeUpdateRequest(
	policy updateAccessPolicy,
	w http.ResponseWriter,
	r *http.Request,
	credential services.CliCredential,
	action apikeyrestrictions.UpdateAction,
	branchName string,
) bool {
	if branchName == "" {
		handlers.RenderError(w, http.StatusForbidden, "This route requires a branch")
		return false
	}
	// KeyID 0 is stateless mode: no API key exists to carry access rules.
	if credential.KeyID != 0 {
		err := policy.AuthorizeUpdates(r.Context(), apikeyrestrictions.UpdateRequest{
			APIKeyContext: apikeyrestrictions.APIKeyContext{
				AppID:    credential.AppID,
				APIKeyID: credential.KeyID,
				ClientIP: helpers.ClientIP(r),
			},
			Branch: branchName,
			Action: action,
		})
		if err != nil {
			handlers.RenderCliAuthError(w, err)
			return false
		}
	}
	return true
}

// publishGroup registers the CLI routes that publish or roll back OTA updates.
type publishGroup struct {
	router       *mux.Router
	cliAuth      *services.CliAuthService
	apiKeyAccess updateAccessPolicy
}

func (g publishGroup) route(method, path string, handler http.HandlerFunc, action apikeyrestrictions.UpdateAction) {
	if !apikeyrestrictions.IsValidUpdateAction(string(action)) {
		panic("router: " + method + " " + path + " was registered with an unknown action " + string(action))
	}
	if !strings.Contains(path, branchVar) {
		panic("router: " + method + " " + path + " is a publish route but names no " + branchVar)
	}
	g.router.Handle(path, g.guard(action, routeBranch)(handler)).Methods(method)
}

// uploadTokenRoute is route() for the one publish route whose branch comes
// from its signed upload token rather than its path. It refuses a path that
// carries a branch variable, since that would be judged through route() instead.
func (g publishGroup) uploadTokenRoute(method, path string, handler http.HandlerFunc) {
	if strings.Contains(path, branchVar) {
		panic("router: " + method + " " + path + " names a " + branchVar +
			", so it must be registered with route(), which judges that branch rather than a token claim")
	}
	g.router.Handle(path, g.guard(apikeyrestrictions.UpdateActionPublish, uploadTokenBranch)(handler)).Methods(method)
}

// branchResolver answers which branch a request acts on.
type branchResolver func(r *http.Request) string

func routeBranch(r *http.Request) string {
	return mux.Vars(r)[branchVarName]
}

// uploadTokenBranch reads the branch out of the upload token, through the
// same validation the handler runs. Anything unreadable, expired,
// foreign-signed or claimless yields "".
func uploadTokenBranch(r *http.Request) string {
	branchName, err := bucket.ResolveUploadTokenBranch(r.URL.Query().Get("token"))
	if err != nil {
		return ""
	}
	return branchName
}

func (g publishGroup) guard(action apikeyrestrictions.UpdateAction, resolveBranch branchResolver) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			credential, err := g.cliAuth.AuthenticateCliCredential(r.Context(), mux.Vars(r)["APP_ID"], helpers.GetAuth(r))
			if err != nil {
				handlers.RenderCliAuthError(w, err)
				return
			}
			if !authorizeUpdateRequest(g.apiKeyAccess, w, r, credential, action, resolveBranch(r)) {
				return
			}
			next.ServeHTTP(w, r.WithContext(services.WithCliAuth(r.Context(), credential)))
		})
	}
}
