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

// branchRulePayload is one rule on the wire; actions are plain strings so an
// unknown one gets a named 400 instead of a decoding error.
type branchRulePayload struct {
	Pattern string   `json:"pattern"`
	Actions []string `json:"actions"`
}

type updatesAccessPayload struct {
	BranchRules []branchRulePayload `json:"branchRules"`
}

type buildAccessPayload struct {
	Actions []BuildAction `json:"actions"`
}

// ApiKeyAccessResponse separates Updates branch rules from app-wide Build grants.
// An empty list grants no access to that domain.
type ApiKeyAccessResponse struct {
	ApiKeyID   string               `json:"apiKeyId"`
	Updates    updatesAccessPayload `json:"updates"`
	Build      buildAccessPayload   `json:"build"`
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
	accesses, err := h.service.GetAccessByApp(r.Context(), appId)
	if err != nil {
		renderApiKeyAccessServiceError(w, err)
		return
	}
	response := make([]ApiKeyAccessResponse, 0, len(accesses))
	for _, access := range accesses {
		entry := ApiKeyAccessResponse{
			ApiKeyID:   strconv.FormatInt(access.ApiKeyID, 10),
			Updates:    updatesAccessPayload{BranchRules: make([]branchRulePayload, 0, len(access.BranchRules))},
			Build:      buildAccessPayload{Actions: append([]BuildAction{}, access.BuildActions...)},
			AllowedIps: make([]string, 0, len(access.AllowedIps)),
		}
		for _, rule := range access.BranchRules {
			entry.Updates.BranchRules = append(entry.Updates.BranchRules, branchRulePayload{
				Pattern: rule.Pattern,
				Actions: fromActions(rule.Actions),
			})
		}
		for _, prefix := range access.AllowedIps {
			entry.AllowedIps = append(entry.AllowedIps, prefix.String())
		}
		response = append(response, entry)
	}
	marshaledResponse, _ := json.Marshal(response)
	// Permission editing must reflect newly created/revoked keys immediately.
	w.Header().Set("Cache-Control", "no-store")
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
		AllowedIps []string              `json:"allowedIps"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccessBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	// Reject stale/partial payloads rather than silently clearing either domain.
	if req.Updates == nil || req.Build == nil || req.Updates.BranchRules == nil || req.Build.Actions == nil || req.AllowedIps == nil {
		handlers.RenderError(w, http.StatusBadRequest, "updates.branchRules, build.actions and allowedIps must be provided as arrays")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	rules := make([]BranchRule, 0, len(req.Updates.BranchRules))
	for _, payload := range req.Updates.BranchRules {
		actions := make([]Action, 0, len(payload.Actions))
		for _, action := range payload.Actions {
			actions = append(actions, Action(action))
		}
		rules = append(rules, BranchRule{Pattern: payload.Pattern, Actions: actions})
	}
	if err := h.service.SetAccess(r.Context(), appId, apiKeyID, rules, req.AllowedIps, req.Build.Actions); err != nil {
		renderApiKeyAccessServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
