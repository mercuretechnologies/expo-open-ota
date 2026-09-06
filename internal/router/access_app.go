package infrastructure

import (
	"net/http"
	"strings"
	"xprem/ee/apikeyrestrictions"
	"xprem/ee/rbac"
	"xprem/internal/handlers"
	"xprem/internal/services"

	"github.com/gorilla/mux"
)

// AppAccess declares whether an Updates token may call an app route and what
// permission a member needs when RBAC is enforced or not.
type AppAccess struct {
	updateToken  bool
	updateAction apikeyrestrictions.UpdateAction
	perm         rbac.Permission
	fallback     rbac.Fallback
	// declared distinguishes a real declaration from the zero value.
	declared bool
}

// AnyViewer allows any account that may see the app.
func AnyViewer() AppAccess {
	return AppAccess{declared: true, perm: rbac.NoPermission}
}

// AnyViewerOrUpdateToken is AnyViewer, plus a token authorized for an Updates
// action on the route's {BRANCH}.
func AnyViewerOrUpdateToken(action apikeyrestrictions.UpdateAction) AppAccess {
	if !apikeyrestrictions.IsValidUpdateAction(string(action)) {
		panic("router: AnyViewerOrUpdateToken called with an unknown action " + string(action))
	}
	return AppAccess{declared: true, perm: rbac.NoPermission, updateToken: true, updateAction: action}
}

// NeedsPermission gates the route behind perm once roles are enforced, and
// behind fallback when they are not.
func NeedsPermission(perm rbac.Permission, fallback rbac.Fallback) AppAccess {
	if perm == rbac.NoPermission {
		panic("router: NeedsPermission called with NoPermission, which gates nothing; use AnyViewer() if that is the intent")
	}
	if fallback != rbac.FallbackAdminOnly && fallback != rbac.FallbackAnyMember {
		panic("router: NeedsPermission called without a Fallback; say what a member gets when roles are not enforced")
	}
	return AppAccess{declared: true, perm: perm, fallback: fallback}
}

// appGroup registers the app-scoped routes.
type appGroup struct {
	router       *mux.Router
	rbacService  *rbac.RBACService
	apiKeyAccess updateAccessPolicy
}

func (g appGroup) route(method, path string, handler http.HandlerFunc, access AppAccess) {
	if !access.declared {
		panic("router: " + method + " " + path + " was registered without an AppAccess declaration")
	}
	if access.updateToken && !strings.Contains(path, branchVar) {
		panic("router: " + method + " " + path + " lets an Updates token in but names no " + branchVar +
			"; Updates permissions are scoped to branches, so a branchless route cannot be judged")
	}
	g.router.Handle(path, g.guard(access)(handler)).Methods(method)
}

// guard turns one AppAccess into the middleware that enforces it.
func (g appGroup) guard(access AppAccess) mux.MiddlewareFunc {
	var permission mux.MiddlewareFunc
	if access.perm != rbac.NoPermission {
		permission = rbac.RequirePermission(g.rbacService, access.perm, access.fallback)
	}

	return func(next http.Handler) http.Handler {
		gated := next
		if permission != nil {
			gated = permission(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if credential := services.CliAuthFromContext(r.Context()); credential != nil {
				if !access.updateToken {
					handlers.RenderError(w, http.StatusForbidden, "This route requires a dashboard session")
					return
				}
				if !authorizeUpdateRequest(g.apiKeyAccess, w, r, *credential, access.updateAction, mux.Vars(r)[branchVarName]) {
					return
				}
				gated.ServeHTTP(w, r)
				return
			}
			gated.ServeHTTP(w, r)
		})
	}
}
