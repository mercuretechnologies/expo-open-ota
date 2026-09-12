package middleware

import (
	"log"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

var buildCapabilityPath = regexp.MustCompile(`(/build-(?:uploads|shares)/)[^/?]+`)

func redactBuildCapability(value string) string {
	redacted := buildCapabilityPath.ReplaceAllString(value, "${1}[REDACTED]")
	if redacted != value {
		if i := strings.IndexByte(redacted, '?'); i >= 0 {
			redacted = redacted[:i] + "?[REDACTED]"
		}
		return redacted
	}
	if decoded, err := url.PathUnescape(value); err == nil && buildCapabilityPath.MatchString(decoded) {
		return "[REDACTED]"
	}
	return value
}

func redactHeaders(headers http.Header) http.Header {
	redactedHeaders := make(http.Header)
	for key, values := range headers {
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "X-Expo-Access-Token") || strings.EqualFold(key, "Expo-Session") || strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "local-upload-token") {
			redactedHeaders[key] = []string{"REDACTED"}
		} else {
			redactedHeaders[key] = append([]string(nil), values...)
			for i, value := range redactedHeaders[key] {
				redactedHeaders[key][i] = redactBuildCapability(value)
			}
		}
	}
	return redactedHeaders
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hc" || r.URL.Path == "/metrics" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		safeHeaders := redactHeaders(r.Header)
		safeURI := redactBuildCapability(r.RequestURI)
		safeQuery := r.URL.RawQuery
		if safeURI != r.RequestURI {
			// A capability URL may echo its token in the query, so the whole query goes.
			safeQuery = "[REDACTED]"
			if i := strings.IndexByte(safeURI, '?'); i >= 0 {
				safeURI = safeURI[:i] + "?[REDACTED]"
			}
		}
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic recovered during %s %s\nQuery: %s\nHeaders: %v\nError: %v\nStack Trace:\n%s",
					r.Method, safeURI, safeQuery, safeHeaders, err, debug.Stack())
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()

		// Telemetry ingestion fires on every app-background of every device;
		// logging each batch would drown the request log.
		if strings.HasSuffix(r.URL.Path, "/v1/logs") || strings.HasSuffix(r.URL.Path, "/v1/metrics") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()

		log.Printf("Started %s %s with query: %s and headers: %v", r.Method, safeURI, safeQuery, safeHeaders)

		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(recorder, r)

		if recorder.statusCode >= 500 {
			log.Printf("Error detected: %s %s returned status %d", r.Method, safeURI, recorder.statusCode)
		}
		log.Printf("Completed %s %d in %v", safeURI, recorder.statusCode, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
