package accountstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func writeDeadline(controller *http.ResponseController, timeout time.Duration) error {
	err := controller.SetWriteDeadline(time.Now().Add(timeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func writeFrame(writer http.ResponseWriter, controller *http.ResponseController, timeout time.Duration, frame Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if err := writeDeadline(controller, timeout); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "id: %s\nevent: %s\ndata: %s\n\n", frame.ID, frame.Type, data); err != nil {
		return err
	}
	return controller.Flush()
}

func writeHeartbeat(writer http.ResponseWriter, controller *http.ResponseController, timeout time.Duration) error {
	if err := writeDeadline(controller, timeout); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(": heartbeat\n\n")); err != nil {
		return err
	}
	return controller.Flush()
}

// Stream writes a subscription as SSE with fixed 15-second heartbeat and
// write deadlines. The subscription is closed on return. Authentication,
// Accept-header validation and maintenance continuation authority remain with
// the route owner.
func (subscription *Subscription) Stream(ctx context.Context, writer http.ResponseWriter) error {
	if subscription == nil || subscription.hub == nil || ctx == nil || writer == nil {
		return ErrClosed
	}
	defer subscription.Close()
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(writer)
	heartbeat := time.NewTicker(subscription.hub.config.heartbeat)
	defer heartbeat.Stop()

	for {
		queued, hasFrame := subscription.popPending()
		if hasFrame {
			closed, err := subscription.writeQueued(writer, controller, queued)
			if err != nil {
				return err
			}
			if closed {
				return nil
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			if err := writeHeartbeat(writer, controller, subscription.hub.config.writeTimeout); err != nil {
				return err
			}
		case queued, ok := <-subscription.queue:
			if !ok {
				return nil
			}
			closed, err := subscription.writeQueued(writer, controller, queued)
			if err != nil {
				return err
			}
			if closed {
				return nil
			}
		}
	}
}

func (subscription *Subscription) writeQueued(writer http.ResponseWriter, controller *http.ResponseController, queued queuedFrame) (bool, error) {
	subscription.deliveryMu.Lock()
	defer subscription.deliveryMu.Unlock()
	if queued.sequence != subscription.sequence.Load() || subscription.closed.Load() {
		return false, nil
	}
	if err := writeFrame(writer, controller, subscription.hub.config.writeTimeout, queued.frame); err != nil {
		return false, err
	}
	return queued.closeAfter, nil
}
