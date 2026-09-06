// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"xprem/internal/cache"
	"xprem/internal/dashboard"
	"xprem/internal/handlers"
	"xprem/internal/validation"

	"github.com/gorilla/mux"
)

// maxAccessBodyBytes bounds the access payload size.
const maxAccessBodyBytes = 64 << 10

type ApiKeyAccessHandler struct {
	service *ApiKeyAccessService
}

func NewApiKeyAccessHandler(service *ApiKeyAccessService) *ApiKeyAccessHandler {
	return &ApiKeyAccessHandler{service: service}
}

type updatesAccessPayload struct {
	Rules []UpdateRule `json:"rules"`
}
type buildAccessPayload struct {
	Rules []BuildRule `json:"rules"`
}
type submitAccessPayload struct {
	Rules []SubmitRule `json:"rules"`
}

// ApiKeyAccessResponse keeps each permission domain and its resource rules explicit.
type ApiKeyAccessResponse struct {
	ApiKeyID   string               `json:"apiKeyId"`
	Updates    updatesAccessPayload `json:"updates"`
	Build      buildAccessPayload   `json:"build"`
	Submit     submitAccessPayload  `json:"submit"`
	AllowedIps []string             `json:"allowedIps"`
}

func renderApiKeyAccessServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRequiresControlPlane), errors.Is(err, ErrInvalidCidr):
		handlers.RenderError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrRequiresValidLicense):
		handlers.RenderError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrApiKeyNotFound):
		handlers.RenderError(w, http.StatusNotFound, err.Error())
	// A malformed rule is caller input, like a malformed CIDR above.
	case validation.IsValidationError(err):
		handlers.RenderError(w, http.StatusBadRequest, err.Error())
	default:
		handlers.RenderError(w, http.StatusInternalServerError, "An internal error occurred.")
	}
}

func (h *ApiKeyAccessHandler) GetApiKeyAccessHandler(w http.ResponseWriter, r *http.Request) {
	appId := mux.Vars(r)["APP_ID"]
	// Cache on the server only; browsers must revalidate after mutations.
	w.Header().Set("Cache-Control", "no-store")
	requestCache := cache.GetCache()
	cacheKey := dashboard.ComputeGetApiKeyAccessCacheKey(appId)
	if cachedValue := requestCache.Get(cacheKey); cachedValue != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(cachedValue))
		return
	}
	accesses, err := h.service.GetAccessByApp(r.Context(), appId)
	if err != nil {
		renderApiKeyAccessServiceError(w, err)
		return
	}
	response := make([]ApiKeyAccessResponse, 0, len(accesses))
	for _, access := range accesses {
		entry := ApiKeyAccessResponse{
			ApiKeyID:   strconv.FormatInt(access.ApiKeyID, 10),
			Updates:    updatesAccessPayload{Rules: append([]UpdateRule{}, access.UpdateRules...)},
			Build:      buildAccessPayload{Rules: append([]BuildRule{}, access.BuildRules...)},
			Submit:     submitAccessPayload{Rules: append([]SubmitRule{}, access.SubmitRules...)},
			AllowedIps: []string{},
		}
		for _, prefix := range access.AllowedIps {
			entry.AllowedIps = append(entry.AllowedIps, prefix.String())
		}
		response = append(response, entry)
	}
	marshaledResponse, _ := json.Marshal(response)
	ttl := 60
	requestCache.Set(cacheKey, string(marshaledResponse), &ttl)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(marshaledResponse)
}

func (h *ApiKeyAccessHandler) SetApiKeyAccessHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	appId := vars["APP_ID"]
	apiKeyID, err := strconv.ParseInt(vars["API_KEY_ID"], 10, 64)
	if err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid API key ID")
		return
	}
	var req struct {
		Updates    *updatesAccessPayload `json:"updates"`
		Build      *buildAccessPayload   `json:"build"`
		Submit     *submitAccessPayload  `json:"submit"`
		AllowedIps []string              `json:"allowedIps"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccessBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	// Reject stale/partial payloads rather than silently clearing either domain.
	if req.Updates == nil || req.Build == nil || req.Submit == nil || req.Updates.Rules == nil || req.Build.Rules == nil || req.Submit.Rules == nil || req.AllowedIps == nil {
		handlers.RenderError(w, http.StatusBadRequest, "updates.rules, build.rules, submit.rules and allowedIps must be provided as arrays")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := h.service.SetAccess(r.Context(), appId, apiKeyID, req.Updates.Rules, req.AllowedIps, req.Build.Rules, req.Submit.Rules); err != nil {
		renderApiKeyAccessServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
