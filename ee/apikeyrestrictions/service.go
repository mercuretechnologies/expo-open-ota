// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"xprem/ee/licensing"
	"xprem/internal/auditlog"
	"xprem/internal/services"
)

// ApiKeyAccess contains per-domain permissions and permitted source networks.
// Empty rule lists grant no access to that domain.
type ApiKeyAccess struct {
	ApiKeyID    int64
	AllowedIps  []netip.Prefix
	UpdateRules []UpdateRule
	BuildRules  []BuildRule
	SubmitRules []SubmitRule
}

// CliRequest is one authenticated CLI request, in the terms the access
// decision is made in.
type CliRequest struct {
	AppID    string
	APIKeyID int64
	Branch   string
	Action   UpdateAction
	ClientIP netip.Addr
}

// ApiKeyAccessRepository persists per-key access. GetAccess is the enforcement
// read on the CLI request hot path.
type ApiKeyAccessRepository interface {
	GetAccessByAppID(ctx context.Context, appID string) ([]ApiKeyAccess, error)
	GetAccess(ctx context.Context, apiKeyID int64) (ApiKeyAccess, error)
	SetAccess(ctx context.Context, appID string, access ApiKeyAccess) error
	// GetApiKeyName resolves the key's display name for the audit trail.
	GetApiKeyName(ctx context.Context, appID string, apiKeyID int64) (string, error)
}

var (
	ErrRequiresControlPlane = errors.New("api key access rules are managed in the database: this deployment runs in stateless mode, which is community edition only")
	ErrRequiresValidLicense = errors.New("api key access rules require an active enterprise license")
	ErrApiKeyNotFound       = errors.New("api key not found")
	ErrInvalidCidr          = errors.New("invalid IP or CIDR range")

	// Both wrap services.ErrCliAccessDenied so the community handlers can map
	// them to a 403 without knowing anything about this package.
	ErrIpNotAllowed = fmt.Errorf("%w: this API key cannot be used from this IP address", services.ErrCliAccessDenied)
)

// deniedError names both the branch and the action so the caller does not
// have to guess which one failed.
func deniedError(action UpdateAction, branchName string) error {
	return fmt.Errorf("%w: this API key is not allowed to %s on branch %q", services.ErrCliAccessDenied, action, branchName)
}

// unjudged turns a repository failure into "could not verify" so an outage
// reaches the CLI as a 500 rather than an invalid-key error. ErrApiKeyNotFound
// passes through unchanged since a missing key is not an outage.
func unjudged(err error) error {
	if errors.Is(err, ErrApiKeyNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", services.ErrCliAuthUnavailable, err)
}

// ApiKeyAccessService owns the management and the enforcement of per-key
// access. Mutations are license-gated; reads are not.
type ApiKeyAccessService struct {
	repo ApiKeyAccessRepository
	// licenseValid reports whether the enterprise license is active.
	licenseValid func() bool
	// onAuditEvent is the audit emission seam; nil means access changes leave
	// no events.
	onAuditEvent auditlog.RecordFunc
}

// NewApiKeyAccessService accepts a nil repository (stateless mode); every
// management method then answers ErrRequiresControlPlane and the enforcement
// is a no-op.
func NewApiKeyAccessService(repo ApiKeyAccessRepository) *ApiKeyAccessService {
	return &ApiKeyAccessService{repo: repo, licenseValid: licensing.IsEnterprise}
}

// SetOnAuditEvent plugs the audit emission seam. Nil-safe.
func (s *ApiKeyAccessService) SetOnAuditEvent(record auditlog.RecordFunc) {
	s.onAuditEvent = record
}

func (s *ApiKeyAccessService) GetAccessByApp(ctx context.Context, appID string) ([]ApiKeyAccess, error) {
	if s.repo == nil {
		return nil, ErrRequiresControlPlane
	}
	return s.repo.GetAccessByAppID(ctx, appID)
}

// SetAccess replaces what one API key is allowed to do. CIDR entries are
// normalized to satisfy the postgres cidr column, and rules are validated and
// reordered by NormalizeUpdateRules.
func (s *ApiKeyAccessService) SetAccess(ctx context.Context, appID string, apiKeyID int64, rules []UpdateRule, cidrs []string, buildRules []BuildRule, submitRules []SubmitRule) error {
	if s.repo == nil {
		return ErrRequiresControlPlane
	}
	if !s.licenseValid() {
		return ErrRequiresValidLicense
	}
	allowedIps, err := parseCidrs(cidrs)
	if err != nil {
		return err
	}
	normalizedRules, err := NormalizeUpdateRules(rules)
	if err != nil {
		return err
	}
	normalizedBuild, err := normalizeBuildRules(buildRules)
	if err != nil {
		return err
	}
	normalizedSubmit, err := normalizeSubmitRules(submitRules)
	if err != nil {
		return err
	}
	access := ApiKeyAccess{
		ApiKeyID:    apiKeyID,
		AllowedIps:  allowedIps,
		UpdateRules: normalizedRules,
		BuildRules:  normalizedBuild,
		SubmitRules: normalizedSubmit,
	}
	if err := s.repo.SetAccess(ctx, appID, access); err != nil {
		return err
	}
	// CIDRs are recorded in normalized form; rules in the form the dashboard shows.
	normalizedCidrs := make([]string, len(allowedIps))
	for i, prefix := range allowedIps {
		normalizedCidrs[i] = prefix.String()
	}
	// Best-effort lookup; the entry names the key while the numeric id stays
	// the stable target id.
	targetDisplay := strconv.FormatInt(apiKeyID, 10)
	if s.onAuditEvent != nil {
		if name, nameErr := s.repo.GetApiKeyName(ctx, appID, apiKeyID); nameErr == nil {
			targetDisplay = name
		}
	}
	s.recordAccessEvent(ctx, auditlog.ActionAPIKeyRestrictionsUpdated,
		"api_key", strconv.FormatInt(apiKeyID, 10), targetDisplay, appID,
		map[string]any{
			"update_rules":  describeUpdateRules(normalizedRules),
			"build_rules":   normalizedBuild,
			"submit_rules":  normalizedSubmit,
			"allowed_cidrs": normalizedCidrs,
		})
	return nil
}

// Authorize is the enforcement point for an authenticated CLI request.
// Without a control plane or an active license, nothing is enforced.
func (s *ApiKeyAccessService) Authorize(ctx context.Context, req CliRequest) error {
	if s.repo == nil || !s.licenseValid() {
		return nil
	}
	access, err := s.repo.GetAccess(ctx, req.APIKeyID)
	if err != nil {
		return unjudged(err)
	}
	if len(access.AllowedIps) > 0 && !ipAllowed(req.ClientIP, access.AllowedIps) {
		return ipNotAllowedError(req.ClientIP)
	}
	if !AllowsUpdates(access.UpdateRules, req.Branch, req.Action) {
		return deniedError(req.Action, req.Branch)
	}
	// A rule that admits a branch name also admits creating that branch via publish.
	return nil
}

// recordAccessEvent reports one access mutation; the actor is the dashboard
// principal on the request context.
func (s *ApiKeyAccessService) recordAccessEvent(ctx context.Context, action auditlog.Action, targetType string, targetID string, targetDisplay string, appID string, metadata map[string]any) {
	if s.onAuditEvent == nil {
		return
	}
	actorID, actorDisplay := "", ""
	if principal := services.PrincipalFromContext(ctx); principal != nil {
		actorID, actorDisplay = principal.UserId, principal.Email
		if actorDisplay == "" {
			actorDisplay = principal.UserId
		}
	}
	s.onAuditEvent(ctx, auditlog.Event{
		ActorType:     auditlog.ActorUser,
		ActorID:       actorID,
		ActorDisplay:  actorDisplay,
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		TargetDisplay: targetDisplay,
		AppID:         appID,
		Outcome:       auditlog.OutcomeSuccess,
		Metadata:      metadata,
	})
}

// ipNotAllowedError wraps ErrIpNotAllowed with the resolved client IP so an
// operator can debug against the allowlist.
func ipNotAllowedError(clientIP netip.Addr) error {
	if clientIP.IsValid() {
		return fmt.Errorf("%w (resolved client IP: %s)", ErrIpNotAllowed, clientIP)
	}
	return fmt.Errorf("%w (the server could not resolve your source IP; if it runs behind a proxy, set TRUST_PROXY_HEADERS)", ErrIpNotAllowed)
}
