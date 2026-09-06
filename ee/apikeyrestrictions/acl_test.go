// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"testing"

	"xprem/internal/validation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchBranchPattern(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"production", "production", true},
		{"production", "Production", false}, // branch names are case sensitive
		{"production", "production-eu", false},
		{"*", "anything", true},
		{"*", "", true},
		{"pr-*", "pr-482", true},
		{"pr-*", "pr-", true},
		{"pr-*", "pr", false},
		{"pr-*", "release-pr-482", false},
		{"*-staging", "eu-staging", true},
		{"*-staging", "staging", false},
		{"pr-*-eu", "pr-482-eu", true},
		{"pr-*-eu", "pr-eu", false}, // the two anchors would have to overlap
		{"ab*ba", "aba", false},
		{"ab*ba", "abba", true},
		{"a*b*c", "azzbzzc", true},
		{"a*b*c", "azzc", false},
		// A literal "[" is a branch name, not a character class.
		{"beta[eu]", "beta[eu]", true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, matchBranchPattern(tc.pattern, tc.name))
		})
	}
}

func TestAllowsBranch_NoRulesMeansNoAccess(t *testing.T) {
	for _, action := range AllUpdateActions {
		assert.False(t, AllowsUpdates(nil, "production", action))
		assert.False(t, AllowsUpdates([]UpdateRule{}, "production", action))
	}
}

func TestAllowsBranch_ScopedKey(t *testing.T) {
	rules := []UpdateRule{
		{Pattern: "production", Actions: []UpdateAction{UpdateActionRead}},
		{Pattern: "staging", Actions: []UpdateAction{UpdateActionPublish}},
		{Pattern: "pr-*", Actions: []UpdateAction{UpdateActionPublish, UpdateActionRollback}},
	}

	assert.True(t, AllowsUpdates(rules, "production", UpdateActionRead))
	assert.False(t, AllowsUpdates(rules, "production", UpdateActionPublish))
	assert.False(t, AllowsUpdates(rules, "production", UpdateActionRollback))

	assert.True(t, AllowsUpdates(rules, "staging", UpdateActionPublish))
	assert.False(t, AllowsUpdates(rules, "staging", UpdateActionRollback))

	assert.True(t, AllowsUpdates(rules, "pr-482", UpdateActionRollback))
	assert.False(t, AllowsUpdates(rules, "develop", UpdateActionRead))
}

func TestAllowsBranch_WriteImpliesRead(t *testing.T) {
	rules := []UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionPublish}}}
	assert.True(t, AllowsUpdates(rules, "staging", UpdateActionRead))

	rollbackOnly := []UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionRollback}}}
	assert.True(t, AllowsUpdates(rollbackOnly, "staging", UpdateActionRead))
	assert.False(t, AllowsUpdates(rollbackOnly, "staging", UpdateActionPublish))
}

func TestAllowsBranch_ScopedKeyIsRefusedWithoutABranch(t *testing.T) {
	rules := []UpdateRule{{Pattern: "*", Actions: []UpdateAction{UpdateActionPublish}}}
	assert.False(t, AllowsUpdates(rules, "", UpdateActionPublish))
	assert.False(t, AllowsUpdates(nil, "", UpdateActionPublish))
}

func TestImplies_UnknownActionGrantsNothing(t *testing.T) {
	rules := []UpdateRule{{Pattern: "production", Actions: []UpdateAction{"delete"}}}
	for _, action := range AllUpdateActions {
		assert.False(t, AllowsUpdates(rules, "production", action),
			"an unrecognised action must not grant %q", action)
	}
}

func TestNormalizeUpdateRules_OrdersAndDeduplicatesActions(t *testing.T) {
	normalized, err := NormalizeUpdateRules([]UpdateRule{{
		Pattern: "staging",
		Actions: []UpdateAction{UpdateActionRollback, UpdateActionRead, UpdateActionRollback},
	}})
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, []UpdateAction{UpdateActionRead, UpdateActionRollback}, normalized[0].Actions)
}

func TestNormalizeUpdateRules_Rejects(t *testing.T) {
	tooMany := make([]UpdateRule, maxUpdateRules+1)
	for i := range tooMany {
		tooMany[i] = UpdateRule{Pattern: string(rune('a' + i%26)), Actions: []UpdateAction{UpdateActionRead}}
	}

	cases := map[string][]UpdateRule{
		"empty pattern":     {{Pattern: "", Actions: []UpdateAction{UpdateActionRead}}},
		"path separator":    {{Pattern: "feature/x", Actions: []UpdateAction{UpdateActionRead}}},
		"control character": {{Pattern: "bad\nname", Actions: []UpdateAction{UpdateActionRead}}},
		"no action":         {{Pattern: "staging"}},
		"unknown action":    {{Pattern: "staging", Actions: []UpdateAction{"delete"}}},
		"duplicate pattern": {
			{Pattern: "staging", Actions: []UpdateAction{UpdateActionRead}},
			{Pattern: "staging", Actions: []UpdateAction{UpdateActionPublish}},
		},
		"too many rules": tooMany,
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeUpdateRules(rules)
			require.Error(t, err)
			// Must be a validation error so the handler answers 400.
			assert.True(t, validation.IsValidationError(err))
		})
	}
}

func TestNormalizeUpdateRules_AcceptsWildcards(t *testing.T) {
	normalized, err := NormalizeUpdateRules([]UpdateRule{
		{Pattern: "*", Actions: []UpdateAction{UpdateActionRead}},
		{Pattern: "pr-*", Actions: []UpdateAction{UpdateActionPublish}},
	})
	require.NoError(t, err)
	assert.Len(t, normalized, 2)
}

// "*" and "**" are the same set of branches, so they collapse to one rule.
func TestNormalizeUpdateRules_CollapsesWildcardRuns(t *testing.T) {
	normalized, err := NormalizeUpdateRules([]UpdateRule{
		{Pattern: "a**b", Actions: []UpdateAction{UpdateActionRead}},
	})
	require.NoError(t, err)
	assert.Equal(t, "a*b", normalized[0].Pattern)

	_, err = NormalizeUpdateRules([]UpdateRule{
		{Pattern: "*", Actions: []UpdateAction{UpdateActionRead}},
		{Pattern: "***", Actions: []UpdateAction{UpdateActionPublish}},
	})
	require.Error(t, err)
	assert.True(t, validation.IsValidationError(err))
}

func TestDescribeUpdateRules(t *testing.T) {
	described := describeUpdateRules([]UpdateRule{
		{Pattern: "pr-*", Actions: []UpdateAction{UpdateActionRead, UpdateActionPublish}},
	})
	assert.Equal(t, []string{"pr-*:read+publish"}, described)
}
