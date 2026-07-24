// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Identity is expected to be quiet in steady state (per-session, mostly
// no-op re-identifies); these metrics exist to see the two failure modes
// coming: latency creep on the hot stat rows during install storms (the
// signal to move stats to a batched aggregator), and a chronic dropped-keys
// count (a client sending keys the operator never declared).
var (
	identityApplyDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "identity_apply_duration_seconds",
			Help:    "Duration of identity operations against the store, by operation and outcome",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		// outcome keeps fast-failing hostile requests from skewing the
		// latency percentiles of legitimate operations down.
		[]string{"op", "outcome"},
	)

	identityApplyVec = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "identity_apply_total",
			Help: "Identity operations applied, by appId, operation and outcome",
		},
		[]string{"appId", "op", "outcome"},
	)

	identityDroppedKeysVec = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "identity_dropped_keys_total",
			Help: "Metadata keys rejected by the allowlist, by appId",
		},
		[]string{"appId"},
	)
)

func init() {
	prometheus.MustRegister(identityApplyDuration, identityApplyVec, identityDroppedKeysVec)
}

func observeApply(appID string, op Op, err error, droppedKeys int, elapsed time.Duration) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
		// The appId only becomes trustworthy once the store accepted it (the
		// device FK guarantees a real app). On errors it may be any string a
		// hostile client sprayed on the unauthenticated wire, and every unique
		// label value allocates a permanent child series in the registry, so
		// error counts are aggregated under a sentinel instead.
		appID = "unknown"
	}
	identityApplyDuration.WithLabelValues(string(op), outcome).Observe(elapsed.Seconds())
	identityApplyVec.WithLabelValues(appID, string(op), outcome).Inc()
	if droppedKeys > 0 && err == nil {
		identityDroppedKeysVec.WithLabelValues(appID).Add(float64(droppedKeys))
	}
}
