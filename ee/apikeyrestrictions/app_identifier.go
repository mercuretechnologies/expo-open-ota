// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"xprem/internal/validation"

	"github.com/google/uuid"
)

func normalizeIdentifierID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", validation.Errorf("appIdentifierId", "a registered app identifier ID is required")
	}
	return id.String(), nil
}
