package pgtest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForPackageLockReturnsImmediatelyWhenLockIsAcquired(t *testing.T) {
	attempts := 0

	err := waitForPackageLock(context.Background(), time.Hour, func(context.Context) (bool, error) {
		attempts++
		return true, nil
	})

	if err != nil {
		t.Fatalf("waitForPackageLock() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("waitForPackageLock() attempts = %d, want 1", attempts)
	}
}

func TestWaitForPackageLockRetriesUntilLockIsAcquired(t *testing.T) {
	attempts := 0

	err := waitForPackageLock(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		attempts++
		return attempts == 2, nil
	})

	if err != nil {
		t.Fatalf("waitForPackageLock() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("waitForPackageLock() attempts = %d, want 2", attempts)
	}
}

func TestWaitForPackageLockReturnsTryLockError(t *testing.T) {
	expectedErr := errors.New("database unavailable")

	err := waitForPackageLock(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		return false, expectedErr
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("waitForPackageLock() error = %v, want %v", err, expectedErr)
	}
}

func TestWaitForPackageLockReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForPackageLock(ctx, time.Hour, func(context.Context) (bool, error) {
		return false, nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForPackageLock() error = %v, want %v", err, context.Canceled)
	}
}
