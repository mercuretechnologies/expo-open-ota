package services

import (
	"encoding/json"
	"strings"
	"time"
	"xprem/internal/validation"
)

// Validate EAS log records without reserializing them: retry offsets refer to the exact bytes sent.
func validateBuildLogEvents(content string) error {
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		var event struct {
			LogID       string    `json:"logId"`
			Time        time.Time `json:"time"`
			Level       int       `json:"level"`
			Message     *string   `json:"msg"`
			Phase       string    `json:"phase"`
			StepID      string    `json:"buildStepId"`
			DisplayName string    `json:"buildStepDisplayName"`
			Marker      string    `json:"marker"`
			Result      string    `json:"result"`
			DurationMs  *int64    `json:"durationMs"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.LogID == "" || event.Time.IsZero() || event.Message == nil || event.Level < 10 || event.Level > 60 || event.Level%10 != 0 {
			return validation.Errorf("logs", "expected complete EAS JSON log records")
		}
		if len(event.LogID) > 255 || len(event.Phase) > 255 || len(event.StepID) > 255 || len(event.DisplayName) > 255 {
			return validation.Errorf("logs", "log identifiers and labels must fit in 255 bytes")
		}
		switch event.Marker {
		case "":
		case "START_PHASE", "END_PHASE":
			if event.Phase == "" {
				return validation.Errorf("logs", "phase markers require phase")
			}
		default:
			return validation.Errorf("logs", "unknown phase marker")
		}
		if event.Marker == "END_PHASE" {
			if event.DurationMs == nil || *event.DurationMs < 0 {
				return validation.Errorf("logs", "phase end requires a nonnegative durationMs")
			}
			switch event.Result {
			case "success", "failed", "warning", "skipped", "unknown":
			default:
				return validation.Errorf("logs", "phase end requires a result")
			}
		}
	}
	return nil
}
