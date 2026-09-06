// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"xprem/internal/services"
)

// APIKeyContext identifies an already authenticated key and its source address.
// Callers derive this from authentication, never from the operation's payload.
// Authorization remains usable outside HTTP (for example before enqueueing a job).
type APIKeyContext struct {
	AppID    string
	APIKeyID int64
	ClientIP netip.Addr
}

// authorizationAccess applies the common checks to every permission domain.
// A nil result means restrictions are disabled in MIT/stateless mode. Otherwise,
// the repository must return a live key in this app, with one consistent policy.
// The dashboard response cache is deliberately not used for enforcement.
func (s *ApiKeyAccessService) authorizationAccess(ctx context.Context, key APIKeyContext) (*ApiKeyAccess, error) {
	if s.repo == nil || !s.licenseValid() {
		return nil, nil
	}
	if strings.TrimSpace(key.AppID) == "" || key.APIKeyID <= 0 {
		return nil, ErrApiKeyNotFound
	}
	access, err := s.repo.GetAccess(ctx, key.AppID, key.APIKeyID)
	if err != nil {
		return nil, unjudged(err)
	}
	if access.ApiKeyID != key.APIKeyID {
		return nil, unjudged(errors.New("permission lookup returned a different API key"))
	}
	if len(access.AllowedIps) > 0 && !ipAllowed(key.ClientIP, access.AllowedIps) {
		return nil, ipNotAllowedError(key.ClientIP)
	}
	return &access, nil
}

// unjudged preserves missing/revoked keys as authentication failures and maps
// repository outages to an unavailable decision, never to an authorization grant.
func unjudged(err error) error {
	if errors.Is(err, ErrApiKeyNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", services.ErrCliAuthUnavailable, err)
}
