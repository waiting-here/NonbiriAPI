package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func (a *Adapter) inferenceStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard, errorContext upstreamerror.Context, request *InferenceRequest, limit int64, lineLimit, eventLimit int) AttemptResult {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{MaxBytes: limit, MaxLineBytes: lineLimit, MaxEventBytes: eventLimit, ReadBuffer: 64 << 10, EventBuffer: 1})
	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	committed := false
	usage := Usage{}
	completedImages := 0
	combinedImageUsage := emptyImageUsage()
	finished := map[int64]bool{}
	responseID := ""
	responseItems := responseStreamSnapshot{}
	defer responseItems.clear()
	writtenBytes := int64(0)
	failure := func(message string) AttemptResult {
		return a.streamProtocolFailure(writer, controller, committed, usage, message)
	}
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
			return contract.ReadFailed(failure("upstream stream ended before completion"), err, ctx)
		}
		raw := []byte(event.Data)
		if event.Event == "error" || upstreamerror.IsEvent(raw) {
			if reported, present := inferenceErrorUsage(raw, request.operation); present {
				usage = reported
			}
			upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
			result := a.streamReportedFailure(writer, controller, committed, usage, guard, errorContext.Parse(raw))
			clear(raw)
			return result
		}
		terminal := false
		kind := ""
		var projected []byte
		if request.operation == contract.OperationCompletions && bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
			clear(raw)
			if len(finished) != request.count {
				return failure("upstream completion was incomplete")
			}
			projected = []byte("[DONE]")
			terminal = true
		} else {
			if !validateEmbeddingJSON(raw) {
				clear(raw)
				return failure("upstream stream event was invalid")
			}
			fields, valid := borrowedObjectFields(raw)
			if !valid {
				clear(raw)
				return failure("upstream stream event was invalid")
			}
			root := fieldsByName(fields)
			if root["model"] != nil {
				root["model"], _ = json.Marshal(request.Model)
			}
			delete(root, "safety_identifier")
			if request.operation != contract.OperationCompletions {
				if json.Unmarshal(root["type"], &kind) != nil || len(kind) > 128 || strings.ContainsAny(kind, "\r\n\x00") {
					clear(raw)
					return failure("upstream stream event was invalid")
				}
				if event.Event != "" && event.Event != "message" && event.Event != kind {
					clear(raw)
					return failure("upstream event type did not match")
				}
			}
			switch request.operation {
			case contract.OperationResponses:
				if !strings.HasPrefix(kind, "response.") {
					clear(raw)
					return failure("upstream response event was invalid")
				}
				if value := root["response_id"]; value != nil {
					var id string
					if json.Unmarshal(value, &id) != nil || !validOpaqueText(id, 512, true) || responseID != "" && id != responseID {
						clear(raw)
						return failure("upstream response identity changed")
					}
					responseID = id
				}
				terminal = kind == "response.completed" || kind == "response.incomplete"
				if !responseItems.observe(kind, root) {
					clear(raw)
					return failure("upstream response item identity or order was invalid")
				}
				if nested := root["response"]; len(nested) > 0 {
					var object map[string]json.RawMessage
					if json.Unmarshal(nested, &object) != nil {
						clear(raw)
						return failure("upstream response event was invalid")
					}
					if value := object["usage"]; len(value) > 0 && !isJSONNull(value) {
						usage = ParseResponseUsage(value)
					}
					if kind == "response.failed" {
						upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
						result := a.streamReportedFailure(writer, controller, committed, usage, guard, errorContext.Parse(nested))
						clear(raw)
						return result
					}
					var id string
					if json.Unmarshal(object["id"], &id) != nil || id == "" || len(id) > 512 || responseID != "" && id != responseID {
						clear(raw)
						return failure("upstream response identity changed")
					}
					responseID = id
					if terminal {
						items, complete := responseItems.completed()
						completed, completionErr := CompleteResponseTerminal(nested, kind, responseID, items)
						if !complete || completionErr != nil {
							clear(completed)
							clear(raw)
							return failure("upstream response completion did not match")
						}
						projected, usage, err = nativeResponseProjection(completed, request)
						clear(completed)
						if err != nil {
							clear(raw)
							return failure("upstream response completion was invalid")
						}
						root["response"] = projected
					} else {
						object["model"], _ = json.Marshal(request.Model)
						object["store"] = json.RawMessage("false")
						object["background"] = json.RawMessage("false")
						delete(object, "safety_identifier")
						root["response"], _ = json.Marshal(object)
					}
				} else if terminal || kind == "response.failed" {
					clear(raw)
					return failure("upstream response completion was missing")
				}
				encoded, encodeErr := json.Marshal(root)
				clear(projected)
				projected, err = encoded, encodeErr
			case contract.OperationCompletions:
				if value := root["usage"]; len(value) > 0 && !isJSONNull(value) {
					usage = ParseChatUsage(value)
				}
				if !validLegacyChoices(root["choices"], true, request.count, finished) {
					clear(raw)
					return failure("upstream completion event was invalid")
				}
				root["model"], _ = json.Marshal(request.Model)
				projected, err = json.Marshal(root)
			case contract.OperationImagesEdits:
				if kind != "image_edit.partial_image" && kind != "image_edit.completed" {
					clear(raw)
					return failure("upstream image event was invalid")
				}
				root["type"], _ = json.Marshal(strings.Replace(kind, "image_edit.", "image_generation.", 1))
				normalized, _ := json.Marshal(root)
				var report Usage
				var done bool
				projected, report, done, err = projectImageResponse(normalized, &ImageRequest{Model: request.Model, Count: request.count}, true)
				clear(normalized)
				if err == nil {
					var object map[string]json.RawMessage
					_ = json.Unmarshal(projected, &object)
					clear(projected)
					object["type"], _ = json.Marshal(kind)
					projected, err = json.Marshal(object)
					if done {
						completedImages++
						combinedImageUsage = combineImageUsage(combinedImageUsage, report)
						terminal = completedImages == request.count
						if terminal {
							usage = combinedImageUsage
						}
					}
				}
			case contract.OperationAudioSpeech:
				if kind != "speech.audio.delta" && kind != "speech.audio.done" {
					clear(raw)
					return failure("upstream speech event was invalid")
				}
				terminal = kind == "speech.audio.done"
				if !terminal && !validImageBase64(root["audio"]) {
					clear(raw)
					return failure("upstream audio data was invalid")
				}
				if terminal {
					usage = ParseAudioUsage(root["usage"])
				}
				projected, err = json.Marshal(root)
			case contract.OperationAudioTranscriptions:
				if kind != "transcript.text.delta" && kind != "transcript.text.segment" && kind != "transcript.text.done" {
					clear(raw)
					return failure("upstream transcription event was invalid")
				}
				textField := "text"
				if kind == "transcript.text.delta" {
					textField = "delta"
				}
				if _, ok := decisionString(root[textField], 0, int(limit)); !ok {
					clear(raw)
					return failure("upstream transcript was invalid")
				}
				terminal = kind == "transcript.text.done"
				if terminal {
					usage = ParseAudioUsage(root["usage"])
				}
				projected, err = json.Marshal(root)
			default:
				clear(raw)
				return failure("upstream stream operation was invalid")
			}
			clear(raw)
			if err != nil || int64(len(projected)) > limit {
				clear(projected)
				return failure("upstream stream event was invalid")
			}
			if guard.ContainsJSON(projected, projected) {
				clear(projected)
				return failure("upstream stream event was rejected")
			}
		}
		frame := []byte{}
		if kind != "" {
			frame = append(frame, []byte("event: "+kind+"\n")...)
		}
		frame = append(frame, []byte("data: ")...)
		frame = append(frame, projected...)
		frame = append(frame, '\n', '\n')
		clear(projected)
		if int64(len(frame)) > limit-writtenBytes {
			clear(frame)
			return failure("upstream stream exceeded its limit")
		}
		writtenBytes += int64(len(frame))
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		clear(frame)
		committed = committed || wrote
		if writeErr != nil {
			result := sinkFailureWithCommit(committed, usage)
			if terminal {
				result = contract.ConfirmedSuccess(result, response.StatusCode)
			}
			return result
		}
		if terminal {
			return AttemptResult{Success: true, Committed: committed, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
		}
	}
}
