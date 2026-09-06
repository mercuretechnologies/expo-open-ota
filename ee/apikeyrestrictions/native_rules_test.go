// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestNativeRulesKeepIdentifiersDestinationsAndActionsSeparate(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	repo := &fakeAccessRepo{}
	service := serviceWith(repo, true)
	err := service.SetAccess(context.Background(), "app", 42, nil, nil,
		[]BuildRule{{AppIdentifierID: strings.ToUpper(id), Actions: []BuildAction{BuildActionCreate, BuildActionCreate}}},
		[]SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationTestFlight, Actions: []SubmitAction{SubmitActionUpload, SubmitActionUpload}}})
	require.NoError(t, err)
	build := repo.setAccess.BuildRules[0]
	require.Equal(t, []BuildAction{BuildActionCreate}, build.Actions)
	assert.True(t, build.Allows(id, BuildActionRead))
	assert.True(t, build.Allows(id, BuildActionCreate))
	assert.False(t, build.Allows(id, BuildActionCancel))
	assert.False(t, build.Allows("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", BuildActionCreate))
	assert.False(t, build.Allows(id, BuildAction("upload")))
	submit := repo.setAccess.SubmitRules[0]
	for _, tc := range []struct {
		identifier  string
		destination SubmitDestination
		action      SubmitAction
		allowed     bool
	}{
		{id, SubmitDestinationTestFlight, SubmitActionRead, true},
		{id, SubmitDestinationTestFlight, SubmitActionUpload, true},
		{id, SubmitDestinationTestFlight, SubmitActionReview, false},
		{id, SubmitDestinationTestFlight, SubmitActionRelease, false},
		{id, SubmitDestinationAppStore, SubmitActionRelease, false},
		{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", SubmitDestinationTestFlight, SubmitActionUpload, false},
		{id, SubmitDestinationTestFlight, SubmitAction("create"), false},
	} {
		assert.Equal(t, tc.allowed, submit.Allows(tc.identifier, tc.destination, tc.action), "%+v", tc)
	}
	// A production upload grant still cannot release the upload to users.
	production := SubmitRule{AppIdentifierID: id, Destination: SubmitDestinationProduction, Actions: []SubmitAction{SubmitActionUpload}}
	assert.True(t, production.Allows(id, SubmitDestinationProduction, SubmitActionUpload))
	assert.False(t, production.Allows(id, SubmitDestinationProduction, SubmitActionRelease))
}

func TestSetAccessRejectsInvalidNativeRulesBeforeWriting(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	build := BuildRule{AppIdentifierID: id, Actions: []BuildAction{BuildActionCreate}}
	submit := SubmitRule{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitActionUpload}}
	for _, tc := range []struct {
		name    string
		builds  []BuildRule
		submits []SubmitRule
	}{
		{"wildcard identifier", []BuildRule{{AppIdentifierID: "*", Actions: build.Actions}}, nil},
		{"empty build actions", []BuildRule{{AppIdentifierID: id}}, nil},
		{"unknown build action", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"release"}}}, nil},
		{"duplicate build identifier", []BuildRule{build, build}, nil},
		{"unknown destination", nil, []SubmitRule{{AppIdentifierID: id, Destination: "*", Actions: submit.Actions}}},
		{"empty submit actions", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal}}},
		{"android review", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitActionReview}}}},
		{"app store upload", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationAppStore, Actions: []SubmitAction{SubmitActionUpload}}}},
		{"duplicate submit destination", nil, []SubmitRule{submit, submit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAccessRepo{}
			err := serviceWith(repo, true).SetAccess(context.Background(), "app", 42, nil, nil, tc.builds, tc.submits)
			require.Error(t, err)
			assert.Zero(t, repo.setCalls)
		})
	}
}
