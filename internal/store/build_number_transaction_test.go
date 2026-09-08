package store_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"xprem/internal/auditlog"
	"xprem/internal/services"
	"xprem/internal/store"
	"xprem/internal/types"

	"github.com/stretchr/testify/require"
)

func TestBuildNumberConcurrentReservations(t *testing.T) {
	_, identifiers, pool := setupCredentialsStores(t)
	ctx := context.Background()
	service := services.NewAppIdentifierService(identifiers)
	app := insertBareApp(t, pool)
	for _, platform := range []types.Platform{types.PlatformAndroid, types.PlatformIOS} {
		t.Run(string(platform), func(t *testing.T) {
			id := insertIdentifier(t, identifiers, app, platform, "com.example.concurrent")
			const count = 24
			audits := make(chan auditlog.Event, count)
			service.SetOnAuditEvent(func(_ context.Context, event auditlog.Event) { audits <- event })
			numbers := make(chan string, count)
			failures := make(chan error, count)
			var workers sync.WaitGroup
			for i := 0; i < count; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					ref, err := service.AllocateBuildNumber(ctx, app, id)
					if err != nil {
						failures <- err
						return
					}
					numbers <- ref
				}()
			}
			workers.Wait()
			close(numbers)
			close(audits)
			for event := range audits {
				previous, err := strconv.Atoi(event.Metadata["from"].(string))
				require.NoError(t, err)
				require.Equal(t, strconv.Itoa(previous+1), event.Metadata["to"])
			}
			close(failures)
			for err := range failures {
				require.NoError(t, err)
			}
			seen := map[string]bool{}
			for number := range numbers {
				require.False(t, seen[number])
				seen[number] = true
			}
			require.Len(t, seen, count)
			for n := 1; n <= count; n++ {
				require.True(t, seen[strconv.Itoa(n)])
			}
			ref, err := identifiers.GetAppIdentifierByID(ctx, app, id)
			require.NoError(t, err)
			require.Equal(t, "24", ref.BuildNumber)
		})
	}
}

func TestBuildNumberSetAndAllocateSerialize(t *testing.T) {
	_, identifiers, pool := setupCredentialsStores(t)
	ctx := context.Background()
	service := services.NewAppIdentifierService(identifiers)
	app := insertBareApp(t, pool)
	for _, platform := range []types.Platform{types.PlatformAndroid, types.PlatformIOS} {
		t.Run(string(platform), func(t *testing.T) {
			id := insertIdentifier(t, identifiers, app, platform, "com.example.race")
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, "SELECT id FROM app_identifiers WHERE id=$1 FOR UPDATE", store.ToPgUUID(id))
			require.NoError(t, err)
			setDone := make(chan error, 1)
			type allocation struct {
				ref string
				err error
			}
			allocated := make(chan allocation, 1)
			go func() { setDone <- identifiers.SetBuildNumber(ctx, app, id, "100") }()
			go func() { ref, err := service.AllocateBuildNumber(ctx, app, id); allocated <- allocation{ref, err} }()
			require.NoError(t, tx.Commit(ctx))
			require.NoError(t, <-setDone)
			got := <-allocated
			require.NoError(t, got.err)
			current, err := identifiers.GetAppIdentifierByID(ctx, app, id)
			require.NoError(t, err)
			if got.ref == "1" {
				require.Equal(t, "100", current.BuildNumber)
			} else {
				require.Equal(t, "101", got.ref)
				require.Equal(t, "101", current.BuildNumber)
			}
		})
	}
}

func TestIOSBuildNumberPreservedAndAllocationRefused(t *testing.T) {
	_, identifiers, pool := setupCredentialsStores(t)
	ctx := context.Background()
	service := services.NewAppIdentifierService(identifiers)
	app := insertBareApp(t, pool)
	id := insertIdentifier(t, identifiers, app, "ios", "com.example.ios")
	for _, tc := range []struct{ previous, next string }{
		{"2100000000", "2100000001"}, {"9223372036854775807", "9223372036854775808"},
		{"99999999999999999999999999999999", "100000000000000000000000000000000"},
	} {
		require.NoError(t, identifiers.SetBuildNumber(ctx, app, id, tc.previous))
		ref, err := service.AllocateBuildNumber(ctx, app, id)
		require.NoError(t, err)
		require.Equal(t, tc.next, ref)
	}
	for _, value := range []string{"1.2.0", "invalid", "-1", "1e3"} {
		// Invalid legacy/corrupt rows must fail without being converted or overwritten.
		require.NoError(t, identifiers.SetBuildNumber(ctx, app, id, value))
		ref, err := service.AllocateBuildNumber(ctx, app, id)
		require.Error(t, err)
		require.Empty(t, ref)
		if value == "1.2.0" {
			require.ErrorIs(t, err, store.ErrDottedBuildNumberAllocationUnsupported)
		}
		current, err := identifiers.GetAppIdentifierByID(ctx, app, id)
		require.NoError(t, err)
		require.Equal(t, value, current.BuildNumber)
	}
	// A refused allocation released its lock and did not poison later reservations.
	require.NoError(t, identifiers.SetBuildNumber(ctx, app, id, "0"))
	ref, err := service.AllocateBuildNumber(ctx, app, id)
	require.NoError(t, err)
	require.Equal(t, "1", ref)
}

func TestBuildNumberTextMigration(t *testing.T) {
	_, _, pool := setupCredentialsStores(t)
	ctx := context.Background()
	migration, err := os.ReadFile("../database/postgres/migrations/20260908120000_build_number_text.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	for _, value := range []string{"42", "1.2.0", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, "CREATE TEMP TABLE app_identifiers (build_number BIGINT NOT NULL DEFAULT 0) ON COMMIT DROP; INSERT INTO app_identifiers VALUES (42),(-9223372036854775808),(9223372036854775807)")
			require.NoError(t, err)
			_, err = tx.Exec(ctx, parts[0])
			require.NoError(t, err)
			var stored []string
			require.NoError(t, tx.QueryRow(ctx, "SELECT array_agg(build_number ORDER BY build_number) FROM app_identifiers").Scan(&stored))
			require.Equal(t, []string{"-9223372036854775808", "42", "9223372036854775807"}, stored)
			var initial string
			require.NoError(t, tx.QueryRow(ctx, "INSERT INTO app_identifiers DEFAULT VALUES RETURNING build_number").Scan(&initial))
			require.Equal(t, "0", initial)
			_, err = tx.Exec(ctx, "INSERT INTO app_identifiers VALUES ($1)", value)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, parts[1])
			if value == "42" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "Cannot restore BIGINT")
			}
		})
	}
}

func TestBuildNumberCalculationErrorRollsBack(t *testing.T) {
	_, identifiers, pool := setupCredentialsStores(t)
	ctx := context.Background()
	app := insertBareApp(t, pool)
	id := insertIdentifier(t, identifiers, app, types.PlatformIOS, "com.example.callback")
	require.NoError(t, identifiers.SetBuildNumber(ctx, app, id, "42"))
	calculationError := errors.New("calculation refused")
	calls := 0
	ref, err := identifiers.AllocateBuildNumber(ctx, app, id, func(platform types.Platform, current string) (string, error) {
		calls++
		require.Equal(t, types.PlatformIOS, platform)
		require.Equal(t, "42", current)
		return "999", calculationError
	})
	require.ErrorIs(t, err, calculationError)
	require.Nil(t, ref)
	require.Equal(t, 1, calls)
	current, err := identifiers.GetAppIdentifierByID(ctx, app, id)
	require.NoError(t, err)
	require.Equal(t, "42", current.BuildNumber)
	// Rollback released the row lock and did not persist the callback's value.
	next, err := services.NewAppIdentifierService(identifiers).AllocateBuildNumber(ctx, app, id)
	require.NoError(t, err)
	require.Equal(t, "43", next)
}
