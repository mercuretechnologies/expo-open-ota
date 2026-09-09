package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildLogsStructuredRecords(t *testing.T) {
	f := newBuildFixture(t)
	ctx := WithCliAuth(context.Background(), CliCredential{AppID: testBuildApp, KeyID: 7})
	_, err := f.service.Start(ctx, testBuildApp, testBuildIdentifier, testBuildID, f.startInput())
	require.NoError(t, err)
	start := `{"logId":"start","time":"2026-09-09T10:00:00Z","level":30,"msg":"Start phase","phase":"RUN_GRADLEW","buildStepId":"gradle","marker":"START_PHASE"}`
	end := `{"logId":"end","time":"2026-09-09T10:00:03Z","level":30,"msg":"End phase","phase":"RUN_GRADLEW","buildStepId":"gradle","marker":"END_PHASE","result":"success","durationMs":3000}`
	content := start + "\n" + end + "\n"
	require.NoError(t, f.service.AppendLogs(ctx, testBuildApp, testBuildIdentifier, testBuildID, 0, content, "ndjson"))
	require.Equal(t, content, f.repo.logs[0].Content)
	require.Equal(t, "ndjson", f.repo.logs[0].Format)
	// Legacy clients omit the format; byte offsets still refer to their original text.
	require.NoError(t, f.service.AppendLogs(ctx, testBuildApp, testBuildIdentifier, testBuildID, int32(len(content)), "old output\n", ""))
	require.Equal(t, "text", f.repo.logs[1].Format)
	for _, invalid := range []string{start[:len(start)-1], start + "\nnot JSON", start + end, "null"} {
		require.Error(t, f.service.AppendLogs(ctx, testBuildApp, testBuildIdentifier, testBuildID, 0, invalid, "ndjson"))
	}
	require.Error(t, f.service.AppendLogs(ctx, testBuildApp, testBuildIdentifier, testBuildID, 0, content, "html"))
	require.Len(t, f.repo.logs, 2)
}

func TestBuildLogsPhaseValidation(t *testing.T) {
	valid := map[string]any{
		"logId": "end", "time": "2026-09-09T10:00:03Z", "level": 30, "msg": "",
		"phase": "RUN_GRADLEW", "buildStepId": "gradle", "buildStepDisplayName": "Run Gradle",
		"marker": "END_PHASE", "result": "success", "durationMs": 3000,
	}
	for field, invalid := range map[string]any{
		"logId": "", "time": "invalid", "level": 31, "msg": nil, "phase": "",
		"buildStepId": strings.Repeat("x", 256), "buildStepDisplayName": strings.Repeat("x", 256),
		"marker": "FINISHED", "result": "done", "durationMs": -1,
	} {
		t.Run(field, func(t *testing.T) {
			event := make(map[string]any, len(valid))
			for key, value := range valid {
				event[key] = value
			}
			event[field] = invalid
			content, err := json.Marshal(event)
			require.NoError(t, err)
			require.Error(t, validateBuildLogEvents(string(content)))
		})
	}
	delete(valid, "buildStepId")
	for _, result := range []string{"success", "failed", "warning", "skipped", "unknown"} {
		valid["result"] = result
		content, err := json.Marshal(valid)
		require.NoError(t, err)
		require.NoError(t, validateBuildLogEvents(string(content)))
	}
}
