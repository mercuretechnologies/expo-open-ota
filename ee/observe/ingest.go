// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"errors"
	"expo-open-ota/ee/identity"
	"expo-open-ota/internal/helpers"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// observeResult labels the batches_total counter; see metrics.go.
const (
	resultAccepted    = "accepted"
	resultBadRequest  = "bad_request"
	resultTooLarge    = "too_large"
	resultUnavailable = "unavailable"
)

// maxLogsBodyBytes caps a /v1/logs body. The SDK sends its whole pending
// backlog uncompressed in one POST with no client-side cap; a realistic batch
// is hundreds of KB, 16MB covers a device coming back from a long offline
// stretch. Beyond it we answer 413: the SDK treats it as a permanent failure
// and drops the batch, which is the point: a 5xx would make the device
// re-send the same oversized poison pill forever.
const maxLogsBodyBytes = 16 << 20

// identityApplyTimeout keeps a stalled store operation from tying up an
// ingestion request indefinitely. Each coalesced operation gets its own bound.
const identityApplyTimeout = 5 * time.Second

// IngestHandler owns the expo-observe ingestion routes. The response contract
// is dictated by the SDK's classification and every arm of it either destroys
// or preserves data on the device:
//
//	2xx              batch deleted on the device
//	429/502/503/504  batch kept, retried later
//	anything else    batch deleted (permanent failure)
//
// Two rules follow. Never answer 500 (a panic destroys a batch: the recover
// arm answers 503), and only answer 503 for genuinely transient conditions
// (a healthy retry, not a poison-pill loop).
type IngestHandler struct {
	// identityService applies $set/$set_once/$unset records. nil in stateless
	// mode (no control plane): identity ops are then acknowledged and dropped,
	// like every other record, so devices never accumulate a backlog.
	identityService *identity.Service
	// telemetry persists the flattened non-identity records in ClickHouse.
	// nil when no ClickHouse is configured: telemetry is then acknowledged,
	// counted and dropped.
	telemetry TelemetrySink
	// branches denormalizes update_id -> branch onto every row; nil leaves
	// the branch column empty.
	branches BranchResolver
	// admission is the free-tier gate: telemetry of devices holding no
	// registry slot is acknowledged and dropped. nil admits everything
	// (stateless mode has no sink anyway).
	admission *DeviceAdmission
}

func NewIngestHandler(identityService *identity.Service, telemetry TelemetrySink, branches BranchResolver, admission *DeviceAdmission) *IngestHandler {
	return &IngestHandler{identityService: identityService, telemetry: telemetry, branches: branches, admission: admission}
}

// admitted filters flattened rows through the free-tier gate, one verdict per
// distinct device per batch (a batch is one device's backlog, so this is one
// Admit call amortized further by its cache). Dropped rows are counted.
func admittedRows[R any](ctx context.Context, admission *DeviceAdmission, appID string, remoteIP string, rows []R, clientID func(R) string) []R {
	if admission == nil || len(rows) == 0 {
		return rows
	}
	verdicts := make(map[string]bool, 1)
	kept := rows[:0]
	for _, row := range rows {
		device := clientID(row)
		verdict, seen := verdicts[device]
		if !seen {
			verdict = admission.Admit(ctx, appID, device, remoteIP)
			verdicts[device] = verdict
		}
		if verdict {
			kept = append(kept, row)
		} else {
			observeRecordsDropped(reasonOverDeviceCap, 1)
		}
	}
	return kept
}

// resolveBranch fills MetricRow/LogRow.Branch; the resolver caches, so the
// per-row call is a map hit for every row after the first of an update.
func (h *IngestHandler) resolveBranch(ctx context.Context, appID, updateID string) string {
	if h.branches == nil {
		return ""
	}
	return h.branches.BranchName(ctx, appID, updateID)
}

// HandleLogs ingests POST /observe/{APP_ID}/{projectId}/v1/logs. Rate
// limiting and app-existence run in middleware ahead of this handler. Today
// the only consumer is identity; telemetry records are acknowledged and
// dropped. When the ClickHouse path lands, its dispatch slots in right after
// the identity split, behind the enterprise license gate (identity stays
// free).
func (h *IngestHandler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	// A panic must not turn into gorilla's 500: 500 destroys the batch on the
	// device, 503 preserves it for a retry.
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("observe: recovered panic in logs ingestion: %v", rec)
			observeBatch(resultUnavailable)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogsBodyBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			observeBatch(resultTooLarge)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		// The body could not be read off the wire: transient, preserve.
		observeBatch(resultUnavailable)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	batch, err := DecodeLogs(body)
	if err != nil {
		// Structurally unreadable JSON: a broken client will not repair
		// itself, 400 (permanent) rather than an eternal retry loop.
		observeBatch(resultBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	appID := mux.Vars(r)["APP_ID"]

	if h.identityService != nil {
		remoteIP := ""
		if clientIP := helpers.ClientIP(r); clientIP.IsValid() {
			remoteIP = clientIP.String()
		}
		requests := identityRequestsFromBatch(batch, appID, remoteIP)
		for _, req := range identity.CoalesceRequests(requests) {
			applyContext, cancelApply := context.WithTimeout(r.Context(), identityApplyTimeout)
			_, err := h.identityService.Apply(applyContext, req)
			cancelApply()
			if err != nil {
				// Store errors are transient (pool exhausted, database down):
				// 503 keeps the batch on the device for a retry. Re-applying the
				// already-committed prefix on that retry is idempotent ($set
				// merges, $unset ignores absent keys), so no double effects.
				log.Printf("observe: identity apply failed: %v", err)
				observeBatch(resultUnavailable)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
	}

	// The telemetry path runs after the identity split: rows are the
	// non-identity records, filtered through the free-tier admission gate.
	// On insert failure, 503 preserves the batch; the identity re-apply on
	// that retry is idempotent, and the identical re-flattened rows carry
	// the same content_hash for query-time dedup.
	rows := FlattenLogs(appID, batch, time.Now().UTC())
	if h.telemetry == nil {
		observeRecordsDropped(reasonTelemetry, len(rows))
	} else {
		remoteIP := ""
		if clientIP := helpers.ClientIP(r); clientIP.IsValid() {
			remoteIP = clientIP.String()
		}
		rows = admittedRows(r.Context(), h.admission, appID, remoteIP, rows, func(row LogRow) string { return row.EASClientID })
		if len(rows) > 0 {
			for i := range rows {
				rows[i].Branch = h.resolveBranch(r.Context(), appID, rows[i].UpdateID)
			}
			if err := h.telemetry.InsertLogs(r.Context(), rows); err != nil {
				log.Printf("observe: clickhouse logs insert failed: %v", err)
				observeBatch(resultUnavailable)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
	}

	observeBatch(resultAccepted)
	w.WriteHeader(http.StatusNoContent)
}

// identityRequestsFromBatch turns a decoded logs batch into the identity
// operations it carries, dropping (and counting) records that cannot be
// attributed or are telemetry, not identity. Pure apart from the drop
// counters, so it is unit-tested directly without an HTTP round-trip.
func identityRequestsFromBatch(batch LogBatch, appID, remoteIP string) []identity.Request {
	var requests []identity.Request
	for _, resource := range batch.Resources {
		clientID, _ := resource.Attributes[EASClientIDKey].(string)
		// A missing or forged client id cannot be attributed to an install:
		// skip those records instead of failing the batch (a non-2xx would
		// also destroy or block every legitimate record around them).
		if _, err := uuid.Parse(clientID); err != nil {
			observeRecordsDropped(reasonForgedClientID, len(resource.Records))
			continue
		}
		for _, record := range resource.Records {
			eventName, _ := record.Attributes[EventNameKey].(string)
			if !identity.IsIdentityOp(eventName) {
				// Telemetry, not identity: the ClickHouse path picks these up
				// after the identity split (and counts them dropped when no
				// sink is configured).
				continue
			}
			if req, ok := identity.RequestFromRecord(appID, clientID, identity.Op(eventName), record.Attributes, remoteIP); ok {
				requests = append(requests, req)
			}
		}
	}
	return requests
}

// HandleMetrics ingests POST /observe/{APP_ID}/{projectId}/v1/metrics: same
// response contract and same pipeline as HandleLogs minus the identity split
// (identity ops only ever arrive on /v1/logs). Without a sink it stays the
// pre-ClickHouse acknowledge-and-drop, skipping even the decode. Rate
// limiting runs in middleware ahead of this handler.
func (h *IngestHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("observe: recovered panic in metrics ingestion: %v", rec)
			observeBatch(resultUnavailable)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}()

	if h.telemetry == nil {
		// Drain within the same cap so keep-alive connections stay reusable.
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxLogsBodyBytes))
		observeBatch(resultAccepted)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogsBodyBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			observeBatch(resultTooLarge)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		observeBatch(resultUnavailable)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	batch, err := DecodeMetrics(body)
	if err != nil {
		observeBatch(resultBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	appID := mux.Vars(r)["APP_ID"]
	remoteIP := ""
	if clientIP := helpers.ClientIP(r); clientIP.IsValid() {
		remoteIP = clientIP.String()
	}
	rows := FlattenMetrics(appID, batch, time.Now().UTC())
	rows = admittedRows(r.Context(), h.admission, appID, remoteIP, rows, func(row MetricRow) string { return row.EASClientID })
	if len(rows) > 0 {
		for i := range rows {
			rows[i].Branch = h.resolveBranch(r.Context(), appID, rows[i].UpdateID)
		}
		if err := h.telemetry.InsertMetrics(r.Context(), rows); err != nil {
			log.Printf("observe: clickhouse metrics insert failed: %v", err)
			observeBatch(resultUnavailable)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}

	observeBatch(resultAccepted)
	w.WriteHeader(http.StatusNoContent)
}
