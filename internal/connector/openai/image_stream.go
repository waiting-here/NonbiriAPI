package openai

import (
	"context"
	"net/http"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func (a *Adapter) imageStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard, errorContext upstreamerror.Context, request *ImageRequest) AttemptResult {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{
		MaxBytes: a.maxImageResponseBytes, MaxLineBytes: int(a.maxImageResponseBytes), MaxEventBytes: int(a.maxImageResponseBytes), ReadBuffer: 64 << 10, EventBuffer: 1,
	})
	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	committed := false
	usage := Usage{}
	combinedUsage := Usage{Present: true}
	completedImages := 0
	for {
		event, ok, err := nextSSEEvent(streamCtx, events, errs)
		if err != nil || !ok {
			if ctx.Err() != nil {
				result := contract.ReadFailed(canceledFailure(), ctx.Err(), ctx)
				result.Committed = committed
				result.ClientStatus = committedStatus(committed)
				result.Usage = usage
				return result
			}
			return contract.ReadFailed(a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream ended before completion"), err, ctx)
		}
		if event.Event == "error" || upstreamerror.IsEvent([]byte(event.Data)) {
			upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), []byte(event.Data))
			return a.streamReportedFailure(writer, controller, committed, usage, guard, errorContext.Parse([]byte(event.Data)))
		}
		raw := []byte(event.Data)
		projected, reported, completed, err := projectImageResponse(raw, request, true)
		clear(raw)
		if err != nil {
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream image event was invalid")
		}
		kind := "image_generation.partial_image"
		if completed {
			kind = "image_generation.completed"
		}
		if (event.Event != "message" && event.Event != kind) || guard.containsImageProjection(projected, true) {
			clear(projected)
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream image event was rejected")
		}
		if completed {
			completedImages++
			combinedUsage = combineImageUsage(combinedUsage, reported)
			if completedImages == request.Count {
				usage = combinedUsage
			}
		}
		allCompleted := completedImages == request.Count
		frame := append([]byte("event: "+kind+"\ndata: "), projected...)
		clear(projected)
		frame = append(frame, '\n', '\n')
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		clear(frame)
		committed = committed || wrote
		if writeErr != nil {
			result := sinkFailureWithCommit(committed, usage)
			if allCompleted {
				result = contract.ConfirmedSuccess(result, response.StatusCode)
			}
			return result
		}
		if allCompleted {
			return AttemptResult{Success: true, Committed: committed, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
		}
	}
}

// A stream reports usage per completed image. Partial completion cannot
// establish the usage of the logical request.
func combineImageUsage(current, next Usage) Usage {
	if !current.Present || !next.Present {
		return Usage{TotalMismatch: current.TotalMismatch || next.TotalMismatch}
	}
	input, inputOK := addChecked(current.UncachedInputTokens, next.UncachedInputTokens)
	output, outputOK := addChecked(current.OutputTokens, next.OutputTokens)
	_, totalOK := addChecked(input, output)
	if !inputOK || !outputOK || !totalOK {
		return Usage{TotalMismatch: true}
	}
	return Usage{Present: true, UncachedInputTokens: input, OutputTokens: output, TotalMismatch: current.TotalMismatch || next.TotalMismatch}
}
