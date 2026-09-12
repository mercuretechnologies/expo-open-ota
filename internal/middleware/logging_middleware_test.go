package middleware

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggingMiddlewareRecoversTelemetryPanics(t *testing.T) {
	for _, path := range []string{
		"/observe/app-1/project-1/v1/logs",
		"/observe/app-1/project-1/v1/metrics",
	} {
		t.Run(path, func(t *testing.T) {
			handler := LoggingMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("boom")
			}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil)
			recorder := httptest.NewRecorder()

			require.NotPanics(t, func() {
				handler.ServeHTTP(recorder, request)
			})
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
		})
	}
}

func TestLoggingMiddlewarePreservesFlush(t *testing.T) {
	// The SSE transport flushes after each event through ResponseController,
	// which must traverse the statusRecorder wrapper (Unwrap) or find Flush.
	handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: hello\n\n"))
		require.NoError(t, http.NewResponseController(w).Flush())

		_, directlyFlushable := w.(http.Flusher)
		require.True(t, directlyFlushable, "http.Flusher must stay visible through the wrapper")
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/mcp", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	require.True(t, recorder.Flushed, "the flush must reach the underlying writer")
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

const uploadGrant = "eyJhbGciOiJIUzI1NiJ9.UPLOADGRANT.sig"

func TestLoggingMiddlewareRedactsLocalUploadHeader(t *testing.T) {
	for _, header := range []string{"local-upload-token", "Local-Upload-Token", "LOCAL-UPLOAD-TOKEN"} {
		for _, panics := range []bool{false, true} {
			logs := captureLogs(t)
			handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, []string{uploadGrant}, r.Header[header], "logging must not alter the request")
				if panics {
					panic("upload failed")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPut, "/app-1/uploadLocalFile", nil)
			request.Header[header] = []string{uploadGrant}
			request.Header.Set("Authorization", "Bearer eoo-secret")
			handler.ServeHTTP(httptest.NewRecorder(), request)
			require.Contains(t, logs.String(), "REDACTED")
			require.NotContains(t, logs.String(), uploadGrant)
			require.NotContains(t, logs.String(), "eoo-secret")
		}
	}
}
