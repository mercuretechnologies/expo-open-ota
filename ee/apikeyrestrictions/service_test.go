// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"xprem/internal/services"
	"xprem/internal/validation"
)

type fakeAccessRepo struct {
	access map[int64]ApiKeyAccess

	setAccess ApiKeyAccess
	setCalls  int
	getCalls  int
}

func (f *fakeAccessRepo) GetAccessByAppID(_ context.Context, _ string) ([]ApiKeyAccess, error) {
	f.getCalls++
	var out []ApiKeyAccess
	for _, access := range f.access {
		out = append(out, access)
	}
	return out, nil
}

func (f *fakeAccessRepo) GetAccess(_ context.Context, _ string, apiKeyID int64) (ApiKeyAccess, error) {
	access, ok := f.access[apiKeyID]
	if !ok {
		return ApiKeyAccess{}, ErrApiKeyNotFound
	}
	return access, nil
}

func (f *fakeAccessRepo) SetAccess(_ context.Context, _ string, access ApiKeyAccess) error {
	f.setCalls++
	f.setAccess = access
	return nil
}

func (f *fakeAccessRepo) GetApiKeyName(_ context.Context, _ string, apiKeyID int64) (string, error) {
	if apiKeyID == 42 {
		return "ci-production", nil
	}
	return "", fmt.Errorf("unknown key")
}

func serviceWith(repo ApiKeyAccessRepository, licensed bool) *ApiKeyAccessService {
	service := NewApiKeyAccessService(repo)
	service.licenseValid = func() bool { return licensed }
	return service
}

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// publish is the request most tests are about; the fields that vary are set
// by the caller.
func publishOn(branchName string) UpdateRequest {
	return UpdateRequest{APIKeyContext: APIKeyContext{AppID: "app", APIKeyID: 1}, Branch: branchName, Action: UpdateActionPublish}
}

func TestStatelessModeAnswersControlPlaneError(t *testing.T) {
	service := serviceWith(nil, true)
	if _, err := service.GetAccessByApp(context.Background(), "app"); !errors.Is(err, ErrRequiresControlPlane) {
		t.Fatalf("expected ErrRequiresControlPlane, got %v", err)
	}
	if err := service.SetAccess(context.Background(), "app", 1, nil, nil, nil, nil); !errors.Is(err, ErrRequiresControlPlane) {
		t.Fatalf("expected ErrRequiresControlPlane, got %v", err)
	}
	// Enforcement is a no-op in stateless mode, never an error.
	if err := service.AuthorizeUpdates(context.Background(), publishOn("main")); err != nil {
		t.Fatalf("expected enforcement no-op, got %v", err)
	}
}

func TestMutationsRequireValidLicense(t *testing.T) {
	repo := &fakeAccessRepo{}
	service := serviceWith(repo, false)
	if err := service.SetAccess(context.Background(), "app", 1, nil, nil, nil, nil); !errors.Is(err, ErrRequiresValidLicense) {
		t.Fatalf("expected ErrRequiresValidLicense, got %v", err)
	}
	if repo.setCalls != 0 {
		t.Fatal("repository must not be touched without a valid license")
	}
}

// Reads stay open even without a valid license.
func TestGetAccessDoesNotRequireLicense(t *testing.T) {
	repo := &fakeAccessRepo{access: map[int64]ApiKeyAccess{7: {ApiKeyID: 7}}}
	service := serviceWith(repo, false)
	accesses, err := service.GetAccessByApp(context.Background(), "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accesses) != 1 || accesses[0].ApiKeyID != 7 {
		t.Fatalf("unexpected access: %+v", accesses)
	}
}

// SetAccess persists CIDR entries and rules in normalized form.
func TestSetAccessPersistsNormalizedInput(t *testing.T) {
	repo := &fakeAccessRepo{}
	service := serviceWith(repo, true)
	err := service.SetAccess(context.Background(), "app", 1,
		[]UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionRollback, UpdateActionRead}}},
		[]string{"192.168.1.5/24", "::ffff:10.1.2.3"}, nil,
		nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedIps := []netip.Prefix{
		netip.MustParsePrefix("192.168.1.0/24"),
		netip.MustParsePrefix("10.1.2.3/32"),
	}
	if !reflect.DeepEqual(repo.setAccess.AllowedIps, expectedIps) {
		t.Fatalf("unexpected allowed ips: %v", repo.setAccess.AllowedIps)
	}
	// Actions come back in catalog order, whatever order they were sent in.
	expectedRules := []UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionRead, UpdateActionRollback}}}
	if !reflect.DeepEqual(repo.setAccess.UpdateRules, expectedRules) {
		t.Fatalf("unexpected rules: %+v", repo.setAccess.UpdateRules)
	}
}

func TestSetAccessRejectsInvalidInput(t *testing.T) {
	repo := &fakeAccessRepo{}
	service := serviceWith(repo, true)

	err := service.SetAccess(context.Background(), "app", 1, nil, []string{"not-an-ip"}, nil, nil)
	if !errors.Is(err, ErrInvalidCidr) {
		t.Fatalf("expected ErrInvalidCidr, got %v", err)
	}
	// A malformed rule must also surface as a validation error, not a 500.
	err = service.SetAccess(context.Background(), "app", 1,
		[]UpdateRule{{Pattern: "feature/x", Actions: []UpdateAction{UpdateActionRead}}}, nil, nil, nil)
	if !validation.IsValidationError(err) {
		t.Fatalf("expected a validation error, got %v", err)
	}
	if repo.setCalls != 0 {
		t.Fatal("repository must not be touched on invalid input")
	}
}

// An empty allowlist must reach the repository as nil.
func TestSetAccessEmptyAllowlistIsNil(t *testing.T) {
	repo := &fakeAccessRepo{}
	service := serviceWith(repo, true)
	if err := service.SetAccess(context.Background(), "app", 1, nil, []string{"", "  "}, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setAccess.AllowedIps != nil {
		t.Fatalf("expected nil allowlist, got %v", repo.setAccess.AllowedIps)
	}
}

func TestAuthorizeEnforcesIpAllowlist(t *testing.T) {
	repo := &fakeAccessRepo{
		access: map[int64]ApiKeyAccess{1: {
			ApiKeyID:    1,
			UpdateRules: []UpdateRule{{Pattern: "*", Actions: AllUpdateActions}},
			AllowedIps:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		}},
	}
	service := serviceWith(repo, true)

	request := publishOn("main")
	request.ClientIP = mustAddr(t, "10.1.2.3")
	if err := service.AuthorizeUpdates(context.Background(), request); err != nil {
		t.Fatalf("allowlisted address rejected: %v", err)
	}
	// The rejection names the resolved caller IP and still wraps ErrIpNotAllowed.
	request.ClientIP = mustAddr(t, "203.0.113.9")
	err := service.AuthorizeUpdates(context.Background(), request)
	if !errors.Is(err, ErrIpNotAllowed) {
		t.Fatalf("expected ErrIpNotAllowed, got %v", err)
	}
	if !errors.Is(err, services.ErrCliAccessDenied) {
		t.Fatalf("expected the error to map to a CLI access denial, got %v", err)
	}
	if !strings.Contains(err.Error(), "203.0.113.9") {
		t.Fatalf("expected the rejected IP in the message, got %q", err.Error())
	}
	// An unresolvable caller address never passes; the message hints at proxy config.
	request.ClientIP = netip.Addr{}
	err = service.AuthorizeUpdates(context.Background(), request)
	if !errors.Is(err, ErrIpNotAllowed) {
		t.Fatalf("expected ErrIpNotAllowed for invalid address, got %v", err)
	}
	if !strings.Contains(err.Error(), "TRUST_PROXY_HEADERS") {
		t.Fatalf("expected a proxy hint for the unresolved IP, got %q", err.Error())
	}
}

// Regression: an allowlist entered in mapped form must admit the caller in
// either mapped or plain IPv4 form.
func TestAuthorizeMatchesAllowlistEnteredInMappedForm(t *testing.T) {
	repo := &fakeAccessRepo{access: map[int64]ApiKeyAccess{}}
	service := serviceWith(repo, true)
	err := service.SetAccess(context.Background(), "app", 1, nil,
		[]string{"::ffff:203.0.113.7", "::ffff:10.0.0.0/104"}, nil,
		nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The fake repo does not wire Set to Get, so feed the stored prefixes back manually.
	repo.access[1] = ApiKeyAccess{ApiKeyID: 1, AllowedIps: repo.setAccess.AllowedIps, UpdateRules: []UpdateRule{{Pattern: "*", Actions: AllUpdateActions}}}

	for _, caller := range []string{"203.0.113.7", "::ffff:203.0.113.7", "10.20.30.40"} {
		request := publishOn("main")
		request.ClientIP = mustAddr(t, caller)
		if err := service.AuthorizeUpdates(context.Background(), request); err != nil {
			t.Fatalf("caller %q: allowlisted address rejected: %v", caller, err)
		}
	}
	for _, caller := range []string{"203.0.113.8", "2001:db8::1"} {
		request := publishOn("main")
		request.ClientIP = mustAddr(t, caller)
		if err := service.AuthorizeUpdates(context.Background(), request); !errors.Is(err, ErrIpNotAllowed) {
			t.Fatalf("caller %q: expected ErrIpNotAllowed, got %v", caller, err)
		}
	}
}

// A key with no Updates rule has no Updates access.
func TestAuthorizeDeniesEverythingWithoutRules(t *testing.T) {
	repo := &fakeAccessRepo{access: map[int64]ApiKeyAccess{1: {ApiKeyID: 1}}}
	service := serviceWith(repo, true)
	for _, action := range AllUpdateActions {
		request := publishOn("production")
		request.Action = action
		if err := service.AuthorizeUpdates(context.Background(), request); !errors.Is(err, services.ErrCliAccessDenied) {
			t.Fatalf("action %q: expected access denied, got %v", action, err)
		}
	}
}

func TestAuthorizeEnforcesUpdateRules(t *testing.T) {
	repo := &fakeAccessRepo{
		access: map[int64]ApiKeyAccess{1: {
			ApiKeyID: 1,
			UpdateRules: []UpdateRule{
				{Pattern: "production", Actions: []UpdateAction{UpdateActionRead}},
				{Pattern: "pr-*", Actions: []UpdateAction{UpdateActionPublish}},
			},
		}},
	}
	service := serviceWith(repo, true)

	// In scope, with the action granted.
	if err := service.AuthorizeUpdates(context.Background(), publishOn("pr-482")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// In scope, but the action is not granted.
	err := service.AuthorizeUpdates(context.Background(), publishOn("production"))
	if !errors.Is(err, services.ErrCliAccessDenied) {
		t.Fatalf("expected a CLI access denial, got %v", err)
	}
	if !strings.Contains(err.Error(), "production") || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("expected the branch and the action in the message, got %q", err.Error())
	}
	// Out of scope entirely.
	if err := service.AuthorizeUpdates(context.Background(), publishOn("staging")); !errors.Is(err, services.ErrCliAccessDenied) {
		t.Fatalf("expected a CLI access denial, got %v", err)
	}
	// Reading production is granted by the rule.
	read := publishOn("production")
	read.Action = UpdateActionRead
	if err := service.AuthorizeUpdates(context.Background(), read); err != nil {
		t.Fatalf("unexpected error on a granted read: %v", err)
	}
}

// A scoped key is refused on a request that names no branch.
func TestAuthorizeRefusesBranchlessRequestForScopedKey(t *testing.T) {
	repo := &fakeAccessRepo{
		access: map[int64]ApiKeyAccess{
			1: {ApiKeyID: 1, UpdateRules: []UpdateRule{{Pattern: "*", Actions: []UpdateAction{UpdateActionPublish}}}},
			2: {ApiKeyID: 2},
		},
	}
	service := serviceWith(repo, true)

	if err := service.AuthorizeUpdates(context.Background(), publishOn("")); !errors.Is(err, services.ErrCliAccessDenied) {
		t.Fatalf("expected a CLI access denial for a scoped key, got %v", err)
	}
	// A key without rules is refused too.
	unscoped := publishOn("")
	unscoped.APIKeyID = 2
	if err := service.AuthorizeUpdates(context.Background(), unscoped); !errors.Is(err, services.ErrCliAccessDenied) {
		t.Fatalf("expected access denied for a key without rules, got %v", err)
	}
}

func TestAuthorizeIsNoOpWithoutValidLicense(t *testing.T) {
	repo := &fakeAccessRepo{
		access: map[int64]ApiKeyAccess{1: {
			ApiKeyID:   1,
			AllowedIps: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			UpdateRules: []UpdateRule{
				{Pattern: "staging", Actions: []UpdateAction{UpdateActionRead}},
			},
		}},
	}
	service := serviceWith(repo, false)
	request := publishOn("production")
	request.ClientIP = mustAddr(t, "203.0.113.9")
	if err := service.AuthorizeUpdates(context.Background(), request); err != nil {
		t.Fatalf("expected community behavior without license, got %v", err)
	}
}
