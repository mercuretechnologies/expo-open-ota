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
	"xprem/internal/cache"
	"xprem/internal/dashboard"
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

// ApiKeyAccessRepository persists per-key access. GetAccess returns a consistent
// policy for a live key in the requested app, excluding native rules whose
// identifier no longer belongs to the app or has an incompatible platform.
type ApiKeyAccessRepository interface {
	GetAccessByAppID(ctx context.Context, appID string) ([]ApiKeyAccess, error)
	GetAccess(ctx context.Context, appID string, apiKeyID int64) (ApiKeyAccess, error)
	SetAccess(ctx context.Context, appID string, access ApiKeyAccess) error
	// GetApiKeyName resolves the key's display name for the audit trail.
	GetApiKeyName(ctx context.Context, appID string, apiKeyID int64) (string, error)
}

var (
	ErrRequiresControlPlane = errors.New("api key access rules are managed in the database: this deployment runs in stateless mode, which is community edition only")
	ErrRequiresValidLicense = errors.New("api key access rules require an active enterprise license")
	ErrApiKeyNotFound       = errors.New("api key not found")
	ErrInvalidCidr          = errors.New("invalid IP or CIDR range")

	// Wraps services.ErrCliAccessDenied so the community handlers can map
	// them to a 403 without knowing anything about this package.
	ErrIpNotAllowed = fmt.Errorf("%w: this API key cannot be used from this IP address", services.ErrCliAccessDenied)
)

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
	cache.GetCache().Delete(dashboard.ComputeGetApiKeyAccessCacheKey(appID))
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
			"build_rules":   describeBuildRules(normalizedBuild),
			"submit_rules":  describeSubmitRules(normalizedSubmit),
			"allowed_cidrs": normalizedCidrs,
		})
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
