// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"errors"
	"expo-open-ota/internal/cache"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBranchResolverCachesPositiveAndNegative(t *testing.T) {
	calls := 0
	resolver := NewBranchResolver(cache.NewLocalCache(), func(_ context.Context, _, updateUUID string) (string, error) {
		calls++
		if updateUUID == "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10" {
			return "main", nil
		}
		return "", nil // unknown update: permanent absence
	})
	ctx := context.Background()

	assert.Equal(t, "main", resolver.BranchName(ctx, "app-1", "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"))
	assert.Equal(t, "main", resolver.BranchName(ctx, "app-1", "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"))
	assert.Equal(t, 1, calls, "positive result cached after the first lookup")

	assert.Equal(t, "", resolver.BranchName(ctx, "app-1", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"))
	assert.Equal(t, "", resolver.BranchName(ctx, "app-1", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"))
	assert.Equal(t, 2, calls, "negative result cached too: update ids are never recycled")
}

func TestBranchResolverDoesNotCacheTransientErrors(t *testing.T) {
	calls := 0
	broken := true
	resolver := NewBranchResolver(cache.NewLocalCache(), func(_ context.Context, _, _ string) (string, error) {
		calls++
		if broken {
			return "", errors.New("connection refused")
		}
		return "main", nil
	})
	ctx := context.Background()

	// While the database is down the batch lands with an empty branch...
	assert.Equal(t, "", resolver.BranchName(ctx, "app-1", "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"))
	// ...and recovery is picked up by the next batch, not poisoned by a cache.
	broken = false
	assert.Equal(t, "main", resolver.BranchName(ctx, "app-1", "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"))
	assert.Equal(t, 2, calls)
}

func TestBranchResolverShortCircuitsEmbeddedBundle(t *testing.T) {
	resolver := NewBranchResolver(cache.NewLocalCache(), func(_ context.Context, _, _ string) (string, error) {
		t.Fatal("the zero update id must never reach the lookup")
		return "", nil
	})
	assert.Equal(t, "", resolver.BranchName(context.Background(), "app-1", ZeroUpdateID))
	assert.Equal(t, "", resolver.BranchName(context.Background(), "app-1", ""))
}
