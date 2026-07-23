package handlers

import (
	"context"
	"errors"
	"expo-open-ota/config"
	"expo-open-ota/internal/helpers"
	"expo-open-ota/internal/services"
	"expo-open-ota/internal/types"
	"log"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

type ExpoProtocolHandler struct {
	protocolService *services.ExpoProtocolService
	// onDeviceSeen, when wired, registers the polling device in the universal
	// device registry (the Observe feature's). A method-value seam like the
	// audit recorder: the composition root wires it when Observe is enabled,
	// and it must never block or fail the manifest path (the wired side runs
	// its registry write in the background).
	onDeviceSeen func(ctx context.Context, appID string, easClientID string, remoteIP string)
}

func NewExpoProtocolHandler(ps *services.ExpoProtocolService) *ExpoProtocolHandler {
	return &ExpoProtocolHandler{protocolService: ps}
}

// SetOnDeviceSeen wires the device-contact recorder; nil (never called) keeps
// manifest polls side-effect free.
func (h *ExpoProtocolHandler) SetOnDeviceSeen(fn func(ctx context.Context, appID string, easClientID string, remoteIP string)) {
	h.onDeviceSeen = fn
}

// resolveAppID returns the app a manifest or asset request targets. The
// expo-app-id header wins when present. When it is absent the caller is a v1
// client that cannot send it, so we fall back to the deploy's legacy app —
// see config.LegacyFallbackAppId, which returns "" when there is none and
// leaves the request to be rejected.
func resolveAppID(r *http.Request) string {
	if appId := r.Header.Get("expo-app-id"); appId != "" {
		return appId
	}
	return config.LegacyFallbackAppId()
}

func (h *ExpoProtocolHandler) HandleManifest(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()

	appId := resolveAppID(r)
	if appId == "" {
		log.Printf("[RequestID: %s] No app id provided", requestID)
		http.Error(w, "No app id provided", http.StatusBadRequest)
		return
	}

	channelName := r.Header.Get("expo-channel-name")
	if channelName == "" {
		log.Printf("[RequestID: %s] No channel name provided", requestID)
		http.Error(w, "No channel name provided", http.StatusBadRequest)
		return
	}

	protocolVersion, err := strconv.ParseInt(r.Header.Get("expo-protocol-version"), 10, 64)
	if err != nil {
		log.Printf("[RequestID: %s] Invalid protocol version: %v", requestID, err)
		http.Error(w, "Invalid protocol version", http.StatusBadRequest)
		return
	}

	platform := r.Header.Get("expo-platform")
	if platform == "" {
		platform = r.URL.Query().Get("platform")
	}
	if platform != "ios" && platform != "android" {
		log.Printf("[RequestID: %s] Invalid platform: %s", requestID, platform)
		http.Error(w, "Invalid platform", http.StatusBadRequest)
		return
	}

	runtimeVersion := r.Header.Get("expo-runtime-version")
	if runtimeVersion == "" {
		runtimeVersion = r.URL.Query().Get("runtimeVersion")
	}
	if runtimeVersion == "" {
		log.Printf("[RequestID: %s] No runtime version provided", requestID)
		http.Error(w, "No runtime version provided", http.StatusBadRequest)
		return
	}

	params := services.ManifestRequestParams{
		RequestID:             requestID,
		AppID:                 appId,
		ChannelName:           channelName,
		Platform:              platform,
		RuntimeVersion:        runtimeVersion,
		ProtocolVersion:       protocolVersion,
		ClientID:              r.Header.Get("EAS-Client-ID"),
		CurrentUpdateID:       r.Header.Get("expo-current-update-id"),
		ExpoFatalError:        r.Header.Get("expo-fatal-error"),
		RecentFailedUpdateIDs: r.Header.Get("Expo-Recent-Failed-Update-Ids"),
	}

	// Every poll is a device contact: the registry (when wired) sees it
	// before the manifest resolution, whose outcome is irrelevant to "this
	// device exists and is alive".
	if h.onDeviceSeen != nil && params.ClientID != "" {
		remoteIP := ""
		if clientIP := helpers.ClientIP(r); clientIP.IsValid() {
			remoteIP = clientIP.String()
		}
		h.onDeviceSeen(r.Context(), appId, params.ClientID, remoteIP)
	}

	result, err := h.protocolService.ResolveManifestBundle(r.Context(), params)
	if err != nil {
		var svcErr *services.ExpoProtocolError
		if errors.As(err, &svcErr) {
			http.Error(w, svcErr.Message, svcErr.StatusCode)
			return
		}
		http.Error(w, "Internal operational error", http.StatusInternalServerError)
		return
	}

	if result.Update == nil {
		log.Printf("[RequestID: %s] No update found for runtimeVersion: %s in branch: %s", requestID, runtimeVersion, result.BranchName)
		h.protocolService.PutNoUpdateAvailableInResponse(w, r, appId, runtimeVersion, protocolVersion, requestID)
		return
	}

	updateType := result.UpdateType
	if updateType == types.NormalUpdate {
		h.protocolService.PutUpdateInResponse(w, r, appId, *result.Update, platform, protocolVersion, requestID)
	} else {
		h.protocolService.PutRollbackInResponse(w, r, appId, *result.Update, platform, protocolVersion, requestID)
	}
}
