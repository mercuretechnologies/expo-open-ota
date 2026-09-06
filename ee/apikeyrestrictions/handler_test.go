// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The management API replaces a whole policy. Old or partial payloads must
// never silently erase permissions when a dashboard stays open across deploys.
func TestAccessPayloadRoundTrip(t *testing.T) {
	const policy = `{"updates":{"branchRules":[{"pattern":"staging","actions":["publish"]}]},"build":{"actions":[]},"allowedIps":[]}`
	cases := []struct {
		name, body string
		status     int
	}{
		{"explicit policy", policy, http.StatusNoContent},
		{"no access", `{"updates":{"branchRules":[]},"build":{"actions":[]},"allowedIps":[]}`, http.StatusNoContent},
		{"old shape", `{"branchRules":[],"allowedIps":[]}`, http.StatusBadRequest},
		{"missing group", `{"updates":{"branchRules":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"missing rules", `{"updates":{},"build":{"actions":[]},"allowedIps":[]}`, http.StatusBadRequest},
		{"null actions", `{"updates":{"branchRules":[]},"build":{"actions":null},"allowedIps":[]}`, http.StatusBadRequest},
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
