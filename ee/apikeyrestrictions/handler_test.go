// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"xprem/internal/cache"
	"xprem/internal/dashboard"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The management API replaces a whole policy. Old or partial payloads must
// never silently erase permissions when a dashboard stays open across deploys.
func TestAccessPayloadRoundTrip(t *testing.T) {
	const policy = `{"updates":{"rules":[{"pattern":"staging","actions":["publish"]}]},"build":{"rules":[]},"submit":{"rules":[]},"allowedIps":[]}`
	cases := []struct {
		name, body string
		status     int
	}{
		{"explicit policy", policy, http.StatusNoContent},
		{"native policy", `{"updates":{"rules":[]},"build":{"rules":[{"appIdentifierId":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","actions":["create"]}]},"submit":{"rules":[{"appIdentifierId":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","destination":"internal","actions":["upload"]}]},"allowedIps":[]}`, http.StatusNoContent},
		{"previous flat build shape", `{"updates":{"branchRules":[]},"build":{"actions":["create"]},"allowedIps":[]}`, http.StatusBadRequest},
		{"missing submit", `{"updates":{"rules":[]},"build":{"rules":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"null submit", `{"updates":{"rules":[]},"build":{"rules":[]},"submit":{"rules":null},"allowedIps":[]}`, http.StatusBadRequest},
		{"no access", `{"updates":{"rules":[]},"build":{"rules":[]},"submit":{"rules":[]},"allowedIps":[]}`, http.StatusNoContent},
		{"old shape", `{"branchRules":[],"allowedIps":[]}`, http.StatusBadRequest},
		{"missing group", `{"updates":{"rules":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"missing rules", `{"updates":{},"build":{"rules":[]},"submit":{"rules":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"null actions", `{"updates":{"rules":[]},"build":{"rules":null},"submit":{"rules":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"unknown action", strings.Replace(policy, `"publish"`, `"create"`, 1), http.StatusBadRequest},
		{"trailing payload", policy + `{}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAccessRepo{}
			handler := NewApiKeyAccessHandler(serviceWith(repo, true))
			request := mux.SetURLVars(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body)), map[string]string{"APP_ID": "app", "API_KEY_ID": "42"})
			response := httptest.NewRecorder()
			handler.SetApiKeyAccessHandler(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status != http.StatusNoContent {
				assert.Zero(t, repo.setCalls)
				return
			}
			require.Equal(t, 1, repo.setCalls)
			repo.access = map[int64]ApiKeyAccess{42: repo.setAccess}
			response = httptest.NewRecorder()
			handler.GetApiKeyAccessHandler(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			assert.JSONEq(t, `[{"apiKeyId":"42",`+tc.body[1:]+`]`, response.Body.String())
		})
	}
}

func TestAccessCacheRefreshesAfterPermissionChanges(t *testing.T) {
	appID := t.Name()
	key := dashboard.ComputeGetApiKeyAccessCacheKey(appID)
	t.Cleanup(func() { cache.GetCache().Delete(key) })
	repo := &fakeAccessRepo{access: map[int64]ApiKeyAccess{42: {ApiKeyID: 42}}}
	service := serviceWith(repo, true)
	handler := NewApiKeyAccessHandler(service)
	read := func() string {
		request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/", nil), map[string]string{"APP_ID": appID})
		response := httptest.NewRecorder()
		handler.GetApiKeyAccessHandler(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		return response.Body.String()
	}
	initial := read()
	assert.Equal(t, initial, read())
	require.Equal(t, 1, repo.getCalls, "second request should use the cache")
	require.NoError(t, service.SetAccess(context.Background(), appID, 42,
		[]UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionPublish}}}, nil, nil, nil))
	repo.access[42] = repo.setAccess
	refreshed := read()
	assert.Contains(t, refreshed, "staging")
	assert.NotEqual(t, initial, refreshed)
	assert.Equal(t, 2, repo.getCalls)
	assert.Equal(t, refreshed, read())
	assert.Equal(t, 2, repo.getCalls)
}
