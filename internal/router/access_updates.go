package infrastructure

import (
	"context"
	"net/http"
	"xprem/ee/apikeyrestrictions"
	"xprem/internal/handlers"
	"xprem/internal/helpers"
	"xprem/internal/services"
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
