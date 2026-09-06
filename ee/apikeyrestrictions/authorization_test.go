// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"xprem/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authorizationIdentifier = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

type authorizationRepo struct {
	fakeAccessRepo
	policy   ApiKeyAccess
	err      error
	reads    int
	appID    string
	apiKeyID int64
}

func (r *authorizationRepo) GetAccess(_ context.Context, appID string, apiKeyID int64) (ApiKeyAccess, error) {
	r.reads++
	r.appID, r.apiKeyID = appID, apiKeyID
	return r.policy, r.err
}

type authorizationCall struct {
	name string
	run  func(*ApiKeyAccessService, APIKeyContext) error
}

func domainAuthorizations() []authorizationCall {
	return []authorizationCall{
		{"updates", func(s *ApiKeyAccessService, key APIKeyContext) error {
			return s.AuthorizeUpdates(context.Background(), UpdateRequest{APIKeyContext: key, Branch: "staging", Action: UpdateActionPublish})
		}},
		{"build", func(s *ApiKeyAccessService, key APIKeyContext) error {
			return s.AuthorizeBuild(context.Background(), BuildRequest{APIKeyContext: key, AppIdentifierID: authorizationIdentifier, Action: BuildActionCreate})
		}},
		{"submit", func(s *ApiKeyAccessService, key APIKeyContext) error {
			return s.AuthorizeSubmit(context.Background(), SubmitRequest{APIKeyContext: key, AppIdentifierID: authorizationIdentifier, Destination: SubmitDestinationInternal, Action: SubmitActionUpload})
		}},
	}
}

func authorizationPolicy() ApiKeyAccess {
	return ApiKeyAccess{
		ApiKeyID:    42,
		UpdateRules: []UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionPublish}}},
		BuildRules:  []BuildRule{{AppIdentifierID: authorizationIdentifier, Actions: []BuildAction{BuildActionCreate}}},
		SubmitRules: []SubmitRule{{AppIdentifierID: authorizationIdentifier, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitActionUpload}}},
	}
}

func TestAuthorizationDoesNotInheritOtherDomains(t *testing.T) {
	all := authorizationPolicy()
	for _, tc := range []struct {
		name   string
		policy ApiKeyAccess
	}{
		{"none", ApiKeyAccess{ApiKeyID: 42}},
		{"updates", ApiKeyAccess{ApiKeyID: 42, UpdateRules: all.UpdateRules}},
		{"build", ApiKeyAccess{ApiKeyID: 42, BuildRules: all.BuildRules}},
		{"submit", ApiKeyAccess{ApiKeyID: 42, SubmitRules: all.SubmitRules}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, domain := range domainAuthorizations() {
				t.Run(domain.name, func(t *testing.T) {
					repo := &authorizationRepo{policy: tc.policy}
					err := domain.run(serviceWith(repo, true), APIKeyContext{AppID: "requested-app", APIKeyID: 42})
					if domain.name == tc.name {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, services.ErrCliAccessDenied)
					}
					assert.Equal(t, "requested-app", repo.appID)
					assert.Equal(t, int64(42), repo.apiKeyID)
					assert.Equal(t, 1, repo.reads)
				})
			}
		})
	}
}

func TestNativeAuthorizationUsesExactIdentifiersAndDestinations(t *testing.T) {
	key := APIKeyContext{AppID: "app", APIKeyID: 42}
	for _, tc := range []struct {
		name        string
		identifier  string
		destination SubmitDestination
		build       bool
		submit      bool
	}{
		{"matching targets", authorizationIdentifier, SubmitDestinationInternal, true, true},
		{"canonical UUID", strings.ToUpper(authorizationIdentifier), SubmitDestinationInternal, true, true},
		{"another identifier", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", SubmitDestinationInternal, false, false},
		{"another destination", authorizationIdentifier, SubmitDestinationProduction, true, false},
		{"another platform destination", authorizationIdentifier, SubmitDestinationTestFlight, true, false},
		{"no destination", authorizationIdentifier, "", true, false},
		{"unknown destination", authorizationIdentifier, "*", true, false},
		{"no identifier", "", SubmitDestinationInternal, false, false},
		{"wildcard identifier", "*", SubmitDestinationInternal, false, false},
		{"invalid identifier", "com.example.app", SubmitDestinationInternal, false, false},
		{"zero UUID", "00000000-0000-0000-0000-000000000000", SubmitDestinationInternal, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := serviceWith(&authorizationRepo{policy: authorizationPolicy()}, true)
			buildErr := service.AuthorizeBuild(context.Background(), BuildRequest{APIKeyContext: key, AppIdentifierID: tc.identifier, Action: BuildActionCreate})
			submitErr := service.AuthorizeSubmit(context.Background(), SubmitRequest{APIKeyContext: key, AppIdentifierID: tc.identifier, Destination: tc.destination, Action: SubmitActionUpload})
			for _, result := range []struct {
				domain  string
				err     error
				allowed bool
			}{{"build", buildErr, tc.build}, {"submit", submitErr, tc.submit}} {
				if result.allowed {
					assert.NoError(t, result.err, result.domain)
				} else {
					assert.ErrorIs(t, result.err, services.ErrCliAccessDenied, result.domain)
				}
			}
		})
	}
}

func TestAuthorizationRejectsUnknownActions(t *testing.T) {
	key := APIKeyContext{AppID: "app", APIKeyID: 42}
	service := serviceWith(&authorizationRepo{policy: authorizationPolicy()}, true)
	for _, action := range []string{"", "unknown", "review", "release"} {
		t.Run(action, func(t *testing.T) {
			assert.ErrorIs(t, service.AuthorizeUpdates(context.Background(), UpdateRequest{APIKeyContext: key, Branch: "staging", Action: UpdateAction(action)}), services.ErrCliAccessDenied)
			assert.ErrorIs(t, service.AuthorizeBuild(context.Background(), BuildRequest{APIKeyContext: key, AppIdentifierID: authorizationIdentifier, Action: BuildAction(action)}), services.ErrCliAccessDenied)
			assert.ErrorIs(t, service.AuthorizeSubmit(context.Background(), SubmitRequest{APIKeyContext: key, AppIdentifierID: authorizationIdentifier, Destination: SubmitDestinationInternal, Action: SubmitAction(action)}), services.ErrCliAccessDenied)
		})
	}
}

func TestAuthorizationSharesSourceNetworkAndRepositoryChecks(t *testing.T) {
	dbErr := errors.New("database connection lost")
	for _, domain := range domainAuthorizations() {
		t.Run(domain.name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				ip      netip.Addr
				repoErr error
				wantErr error
			}{
				{"allowed source", netip.MustParseAddr("10.1.2.3"), nil, nil},
				{"denied source", netip.MustParseAddr("203.0.113.1"), nil, ErrIpNotAllowed},
				{"unresolved source", netip.Addr{}, nil, ErrIpNotAllowed},
				{"missing or revoked key", netip.MustParseAddr("10.1.2.3"), ErrApiKeyNotFound, ErrApiKeyNotFound},
				{"unavailable database", netip.MustParseAddr("10.1.2.3"), dbErr, services.ErrCliAuthUnavailable},
			} {
				t.Run(tc.name, func(t *testing.T) {
					policy := authorizationPolicy()
					policy.AllowedIps = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
					repo := &authorizationRepo{policy: policy, err: tc.repoErr}
					err := domain.run(serviceWith(repo, true), APIKeyContext{AppID: "app", APIKeyID: 42, ClientIP: tc.ip})
					if tc.wantErr == nil {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, tc.wantErr)
						if tc.wantErr == ErrIpNotAllowed {
							assert.ErrorIs(t, err, services.ErrCliAccessDenied)
						}
						if tc.repoErr != nil {
							assert.NotErrorIs(t, err, services.ErrCliAccessDenied)
						}
					}
					assert.Equal(t, 1, repo.reads)
				})
			}
		})
	}
}

func TestAuthorizationRequiresKeyIdentityInEnterprise(t *testing.T) {
	for _, key := range []APIKeyContext{{APIKeyID: 42}, {AppID: "  ", APIKeyID: 42}, {AppID: "app"}, {AppID: "app", APIKeyID: -1}} {
		for _, domain := range domainAuthorizations() {
			repo := &authorizationRepo{policy: authorizationPolicy()}
			assert.ErrorIs(t, domain.run(serviceWith(repo, true), key), ErrApiKeyNotFound, "%s: %+v", domain.name, key)
			assert.Zero(t, repo.reads)
		}
	}
}

func TestAuthorizationIsNoOpWithoutEnterprisePolicy(t *testing.T) {
	// Even malformed requests cannot accidentally make MIT/stateless mode depend
	// on the enterprise repository or its rule validation.
	for _, tc := range []struct {
		name string
		run  func(*ApiKeyAccessService) error
	}{
		{"updates", func(s *ApiKeyAccessService) error { return s.AuthorizeUpdates(context.Background(), UpdateRequest{}) }},
		{"build", func(s *ApiKeyAccessService) error { return s.AuthorizeBuild(context.Background(), BuildRequest{}) }},
		{"submit", func(s *ApiKeyAccessService) error { return s.AuthorizeSubmit(context.Background(), SubmitRequest{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &authorizationRepo{err: errors.New("repository must not be consulted")}
			assert.NoError(t, tc.run(serviceWith(repo, false)))
			assert.Zero(t, repo.reads)
			assert.NoError(t, tc.run(serviceWith(nil, true)))
		})
	}
}
