// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRulesKeepIdentifiersAndActionsSeparate(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	repo := &fakeAccessRepo{}
	err := serviceWith(repo, true).SetAccess(context.Background(), "app", 42, nil, nil,
		[]BuildRule{{AppIdentifierID: strings.ToUpper(id), Actions: []BuildAction{BuildActionCreate, BuildActionCreate}}}, nil)
	require.NoError(t, err)
	build := repo.setAccess.BuildRules[0]
	require.Equal(t, []BuildAction{BuildActionCreate}, build.Actions)
	assert.False(t, build.Allows(id, BuildAction("read")))
	assert.True(t, build.Allows(id, BuildActionCreate))
	assert.False(t, build.Allows(id, BuildAction("cancel")))
	assert.False(t, build.Allows("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", BuildActionCreate))
	assert.False(t, build.Allows(id, BuildAction("upload")))
}

func TestSetAccessRejectsInvalidBuildRulesBeforeWriting(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	build := BuildRule{AppIdentifierID: id, Actions: []BuildAction{BuildActionCreate}}
	for _, tc := range []struct {
		name  string
		rules []BuildRule
	}{
		{"wildcard identifier", []BuildRule{{AppIdentifierID: "*", Actions: build.Actions}}},
		{"empty build actions", []BuildRule{{AppIdentifierID: id}}},
		{"removed build read", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"read"}}}},
		{"removed build cancel", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"cancel"}}}},
		{"unknown build action", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"release"}}}},
		{"duplicate build identifier", []BuildRule{build, build}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAccessRepo{}
			err := serviceWith(repo, true).SetAccess(context.Background(), "app", 42, nil, nil, tc.rules, nil)
			require.Error(t, err)
			assert.Zero(t, repo.setCalls)
		})
	}
}
