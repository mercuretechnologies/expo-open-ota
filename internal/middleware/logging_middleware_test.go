package middleware

import (
	"context"
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
