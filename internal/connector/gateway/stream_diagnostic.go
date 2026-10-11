package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// Retain only the rejected event, never a transcript of successful output.
// Locally detected protocol errors use the same private diagnostic capability
// as explicit upstream errors. The public failure message stays source-free.
func captureStreamFailure(ctx context.Context, response *http.Response, reason, event string, started, terminal bool, textBlocks, toolCalls int) {
	if !upstreamerror.IsCapturing(ctx) {
		return
	}
	const eventLimit = 64 << 10
	truncated := len(event) > eventLimit
	if truncated {
		event = event[:eventLimit]
	}
	body, _ := json.Marshal(struct {
		Error       string `json:"error"`
		Event       string `json:"rejected_event,omitempty"`
		Truncated   bool   `json:"event_truncated,omitempty"`
		Started     bool   `json:"stream_started"`
		Terminal    bool   `json:"finish_received"`
		TextBlocks  int    `json:"text_blocks"`
		ToolCalls   int    `json:"tool_calls"`
		ContentType string `json:"upstream_content_type"`
	}{reason, event, truncated, started, terminal, textBlocks, toolCalls, response.Header.Get("Content-Type")})
	defer clear(body)
	upstreamerror.CaptureEvent(ctx, response.StatusCode, "application/json", body)
}
