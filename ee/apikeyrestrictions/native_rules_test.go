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
	assert.False(t, build.Allows(id, BuildAction("read")))
	assert.True(t, build.Allows(id, BuildActionCreate))
	assert.False(t, build.Allows(id, BuildAction("cancel")))
	assert.False(t, build.Allows("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", BuildActionCreate))
	assert.False(t, build.Allows(id, BuildAction("upload")))
	submit := repo.setAccess.SubmitRules[0]
	for _, tc := range []struct {
		identifier  string
		destination SubmitDestination
		action      SubmitAction
		allowed     bool
	}{
		{id, SubmitDestinationTestFlight, SubmitAction("read"), false},
		{id, SubmitDestinationTestFlight, SubmitActionUpload, true},
		{id, SubmitDestinationTestFlight, SubmitAction("review"), false},
		{id, SubmitDestinationTestFlight, SubmitAction("release"), false},
		{id, SubmitDestinationProduction, SubmitActionUpload, false},
		{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", SubmitDestinationTestFlight, SubmitActionUpload, false},
		{id, SubmitDestinationTestFlight, SubmitAction("create"), false},
	} {
		assert.Equal(t, tc.allowed, submit.Allows(tc.identifier, tc.destination, tc.action), "%+v", tc)
	}

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
		{"removed build read", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"read"}}}, nil},
		{"removed build cancel", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"cancel"}}}, nil},
		{"removed submit read", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{"read"}}}},
		{"removed submit release", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{"release"}}}},
		{"unknown build action", []BuildRule{{AppIdentifierID: id, Actions: []BuildAction{"release"}}}, nil},
		{"duplicate build identifier", []BuildRule{build, build}, nil},
		{"unknown destination", nil, []SubmitRule{{AppIdentifierID: id, Destination: "*", Actions: submit.Actions}}},
		{"empty submit actions", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal}}},
		{"removed submit review", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitAction("review")}}}},
		{"app store upload", nil, []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestination("app-store"), Actions: []SubmitAction{SubmitActionUpload}}}},
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
