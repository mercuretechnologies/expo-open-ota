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

func TestSubmitRulesKeepIdentifiersDestinationsAndActionsSeparate(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	repo := &fakeAccessRepo{}
	err := serviceWith(repo, true).SetAccess(context.Background(), "app", 42, nil, nil, nil,
		[]SubmitRule{{AppIdentifierID: strings.ToUpper(id), Destination: SubmitDestinationTestFlight, Actions: []SubmitAction{SubmitActionUpload, SubmitActionUpload}}})
	require.NoError(t, err)
	require.Equal(t, []SubmitAction{SubmitActionUpload}, repo.setAccess.SubmitRules[0].Actions)
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

func TestSetAccessRejectsInvalidSubmitRulesBeforeWriting(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	submit := SubmitRule{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitActionUpload}}
	for _, tc := range []struct {
		name  string
		rules []SubmitRule
	}{
		{"removed submit read", []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{"read"}}}},
		{"removed submit release", []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{"release"}}}},
		{"unknown destination", []SubmitRule{{AppIdentifierID: id, Destination: "*", Actions: submit.Actions}}},
		{"empty submit actions", []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal}}},
		{"removed submit review", []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitAction("review")}}}},
		{"app store upload", []SubmitRule{{AppIdentifierID: id, Destination: SubmitDestination("app-store"), Actions: []SubmitAction{SubmitActionUpload}}}},
		{"duplicate submit destination", []SubmitRule{submit, submit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAccessRepo{}
			err := serviceWith(repo, true).SetAccess(context.Background(), "app", 42, nil, nil, nil, tc.rules)
			require.Error(t, err)
			assert.Zero(t, repo.setCalls)
		})
	}
}
