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
const shareToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoggingMiddlewareRedactsBuildCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, method, target string
		status               int
	}{
		{"upload grant", http.MethodPut, "/build-uploads/" + uploadGrant, http.StatusNoContent},
		{"upload grant with query", http.MethodPut, "/build-uploads/" + uploadGrant + "?debug=" + uploadGrant, http.StatusNoContent},
		{"share page", http.MethodGet, "/build-shares/" + shareToken, http.StatusOK},
		{"encoded slash", http.MethodGet, "/build-shares%2F" + shareToken, http.StatusOK},
		{"encoded route", http.MethodGet, "/build-%73hares/" + shareToken, http.StatusOK},
		{"encoded query link", http.MethodGet, "/api?next=%2Fbuild-shares%2F" + shareToken, http.StatusOK},
		{"share download", http.MethodGet, "/build-shares/" + shareToken + "/download", http.StatusOK},
		{"share under sub path", http.MethodGet, "/ota/build-shares/" + shareToken + "/download", http.StatusOK},
		{"server error", http.MethodGet, "/build-shares/" + shareToken, http.StatusInternalServerError},
		{"token in another query", http.MethodGet, "/api/app/app-1?next=/build-shares/" + shareToken, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			request := httptest.NewRequest(tc.method, tc.target, nil)
			request.Header.Set("Referer", "https://ota.example.com/build-shares/"+shareToken)
			request.Header.Set("Authorization", "Bearer eoo_secret")
			request.Header.Set("Expo-Session", "session-secret")
			request.Header.Set("Cookie", "session=cookie-secret")
			request.Header.Set("User-Agent", "eoas/2.0")
			request.Header.Set("X-Link", "https://ota.example.com/build-shares/"+shareToken+"?echo="+shareToken)
			request.Header.Set("X-Encoded-Link", "https://ota.example.com/build-shares%2F"+shareToken)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code)
			output := logs.String()
			require.Contains(t, output, "Started "+tc.method)
			require.Contains(t, output, "Completed")
			require.Contains(t, output, "[REDACTED]")
			require.Contains(t, output, "eoas/2.0", "ordinary headers stay visible")
			for _, secret := range []string{uploadGrant, shareToken, "UPLOADGRANT", "eoo_secret", "session-secret", "cookie-secret"} {
				require.NotContains(t, output, secret)
			}
			if tc.status >= 500 {
				require.Contains(t, output, "Error detected")
			}
		})
	}
}

func TestLoggingMiddlewareRedactsCapabilityInPanic(t *testing.T) {
	logs := captureLogs(t)
	handler := LoggingMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("upload failed")
	}))
	request := httptest.NewRequest(http.MethodPut, "/build-uploads/"+uploadGrant+"?x=1", nil)
	request.Header.Set("Referer", "https://ota.example.com/build-shares/"+shareToken)
	recorder := httptest.NewRecorder()
	require.NotPanics(t, func() { handler.ServeHTTP(recorder, request) })
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	output := logs.String()
	require.Contains(t, output, "Panic recovered")
	require.Contains(t, output, "/build-uploads/[REDACTED]")
	require.NotContains(t, output, uploadGrant)
	require.NotContains(t, output, shareToken)
}

func TestLoggingMiddlewareKeepsOrdinaryQueries(t *testing.T) {
	logs := captureLogs(t)
	handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/app-1/build/id-1/environment?channel=production", nil)
	request.Header.Set("Authorization", "Bearer eoo_secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	output := logs.String()
	require.Contains(t, output, "channel=production")
	require.Contains(t, output, "/app-1/build/id-1/environment?channel=production")
	require.NotContains(t, output, "/[REDACTED]")
	require.NotContains(t, output, "?[REDACTED]")
	require.NotContains(t, output, "eoo_secret")
	require.Contains(t, output, "Authorization:[REDACTED]")
}

func TestRedactBuildCapability(t *testing.T) {
	require.Equal(t, "/build-uploads/[REDACTED]", redactBuildCapability("/build-uploads/"+uploadGrant))
	require.Equal(t, "/build-shares/[REDACTED]/download", redactBuildCapability("/build-shares/"+shareToken+"/download"))
	require.Equal(t, "https://h/x/build-shares/[REDACTED]?[REDACTED]", redactBuildCapability("https://h/x/build-shares/"+shareToken+"?a=1"))
	require.Equal(t, "/build-shares/", redactBuildCapability("/build-shares/"))
	require.Equal(t, "/api/app/app-1/builds/b-1/shares", redactBuildCapability("/api/app/app-1/builds/b-1/shares"))
}
