package imageactivity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type responseData struct {
	seq    int
	body   []byte
	status int
	header http.Header
	err    error
}

func (responseData) String() string { return "[private image response]" }
func waitContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) perform(ctx context.Context, snapshot upstreamSnapshot, method, target string, payload []byte, profile egress.ImageProfile, ref observability.DiagnosticRef, authorize bool) responseData {
	out := responseData{seq: ref.AttemptSeq}
	clientBase := snapshot.baseURL
	if !authorize {
		parsed, err := url.Parse(target)
		if err != nil {
			out.err = ErrInvalid
			return out
		}
		clientBase = parsed.Scheme + "://" + parsed.Host
	}
	client, err := s.config.Egress.NewImageClient(clientBase, profile)
	if err != nil {
		out.err = err
		return out
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		out.err = err
		return out
	}
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	if profile == egress.ImageDownload {
		req.Header.Set("Accept", "image/png, image/jpeg, image/webp")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorize {
		key, e := s.openSecret(snapshot)
		if e != nil {
			out.err = e
			return out
		}
		req.Header.Set("Authorization", "Bearer "+string(key))
		clear(key)
	}
	ctx = s.config.Diagnostics.ErrorScope(ctx, ref)
	req = req.WithContext(ctx)
	response, err := client.Do(req)
	if err != nil {
		out.err = &transportFailure{cause: err}
		captureTransportFailure(ctx, err)
		return out
	}
	defer response.Body.Close()
	out.status = response.StatusCode
	out.header = response.Header
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		upstreamerror.Context{}.ReadResponse(ctx, response)
		out.err = ErrUnavailable
		return out
	}
	limit := maxResponse
	if profile == egress.ImageDownload {
		limit = maxImage
	} else if profile == egress.ImageMetadata {
		limit = 1 << 20
	}
	out.body, out.err = readBounded(response.Body, limit)
	if out.err != nil {
		s.captureImageOmission(ctx, ref, out, "unreadable_response")
	}
	return out
}
func (s *Service) taskPermit(ctx context.Context, id string, deadline int64) (taskRow, int, error) {
	for {
		now, err := s.now()
		if err != nil {
			return taskRow{}, 0, err
		}
		if now >= deadline {
			return taskRow{}, 0, context.DeadlineExceeded
		}
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			return taskRow{}, 0, err
		}
		row, err := taskTx(ctx, tx, id)
		if err != nil {
			tx.Rollback()
			return row, 0, err
		}
		if row.upstreamRevision == 0 || row.controlID == "" {
			tx.Rollback()
			return row, 0, ErrNotFound
		}
		if row.httpSeq >= 2147483647 {
			tx.Rollback()
			return row, 0, ErrCapacity
		}
		ok, retry, err := reserveHTTPTx(ctx, tx, row.controlID, s.config.Now().UnixMilli())
		if err != nil {
			tx.Rollback()
			return row, 0, err
		}
		if !ok {
			tx.Rollback()
			delay := time.Duration(retry-s.config.Now().UnixMilli()) * time.Millisecond
			if delay > time.Second {
				delay = time.Second
			}
			if delay < time.Millisecond {
				delay = time.Millisecond
			}
			if !waitContext(ctx, delay) {
				return row, 0, ctx.Err()
			}
			continue
		}
		seq := row.httpSeq + 1
		err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_tasks SET http_seq=?,updated_at=? WHERE id=? AND http_seq=?", seq, now, id, row.httpSeq))
		if err != nil {
			tx.Rollback()
			return row, 0, err
		}
		if err = tx.Commit(); err != nil {
			return row, 0, err
		}
		return row, seq, nil
	}
}
func (s *Service) taskGET(ctx context.Context, row taskRow, snapshot upstreamSnapshot, target string, profile egress.ImageProfile, authorize bool, deadline int64) responseData {
	_, seq, err := s.taskPermit(ctx, row.id, deadline)
	if err != nil {
		return responseData{err: err}
	}
	now, err := s.now()
	if err != nil {
		return responseData{err: err}
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(deadline-now)*time.Second)
	defer cancel()
	return s.perform(bounded, snapshot, http.MethodGet, target, nil, profile, observability.DiagnosticRef{TaskID: row.id, AttemptSeq: seq}, authorize)
}
func downloadTarget(raw string, snapshot upstreamSnapshot) (string, error) {
	if len(raw) > 8192 {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Scheme == "" || u.Host == "" || strings.ContainsAny(raw, "\r\n") {
		return "", ErrInvalid
	}
	_, origin, err := egress.CanonicalEndpointTarget(u.Scheme + "://" + u.Host)
	if err != nil {
		return "", ErrInvalid
	}
	allowed := false
	for _, entry := range snapshot.origins {
		if entry == origin {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", ErrInvalid
	}
	return u.String(), nil
}
func (s *Service) extractImages(ctx context.Context, row taskRow, snapshot upstreamSnapshot, body []byte, deadline int64) []imageBytes {
	output := []imageBytes{}
	array, err := rawPointer(body, snapshot.adapter.Response.ImagesPointer, true)
	if err != nil {
		return output
	}
	items, _ := rawArray(array, 16)
	size := 0
	limit := row.n
	if limit <= 0 {
		limit = 16
	}
	for _, item := range items {
		if len(output) >= limit {
			break
		}
		var data []byte
		var declared string
		if pointer := snapshot.adapter.Response.Base64Pointer; pointer != nil {
			raw, e := rawPointer(item, *pointer, false)
			if e == nil {
				data, e = decodeImageBase64(raw)
				if e != nil {
					data = nil
				}
			}
		}
		if data == nil && snapshot.adapter.Response.URLPointer != nil {
			raw, e := rawString(item, *snapshot.adapter.Response.URLPointer, 8192)
			if e != nil {
				continue
			}
			target, e := downloadTarget(raw, snapshot)
			if e != nil {
				continue
			}
			response := s.taskGET(ctx, row, snapshot, target, egress.ImageDownload, false, deadline)
			if response.err != nil {
				continue
			}
			data = make([]byte, len(response.body))
			copy(data, response.body)
			declared = response.header.Get("Content-Type")
		}
		if len(data) > maxImages-size {
			continue
		}
		img, e := checkedImage(data, declared)
		if e != nil {
			continue
		}
		size += len(data)
		output = append(output, img)
	}
	return output
}
func responseState(body []byte, a ResponseAdapter) (string, error) {
	if end, err := valueEnd(body, 0, 0); err != nil || whitespace(body, end) != len(body) {
		return "", ErrInvalid
	}
	if a.StatePointer == nil {
		return "succeeded", nil
	}
	status, err := rawString(body, *a.StatePointer, 128)
	if err != nil {
		return "", err
	}
	for _, s := range a.WorkingStates {
		if status == s {
			return "running", nil
		}
	}
	for _, s := range a.SuccessStates {
		if status == s {
			return "succeeded", nil
		}
	}
	for _, s := range a.FailureStates {
		if status == s {
			return "failed", nil
		}
	}
	return "", ErrInvalid
}
func retrySeconds(header http.Header, now int64) int64 {
	value := header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		if seconds > 60 {
			return 60
		}
		return seconds
	}
	if t, err := http.ParseTime(value); err == nil {
		d := t.Unix() - now
		if d > 60 {
			return 60
		}
		if d > 0 {
			return d
		}
	}
	return 0
}
func readFailure(err error) string {
	var large *egress.ResponseTooLargeError
	if errors.Is(err, ErrCapacity) || errors.As(err, &large) {
		return "response_too_large"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "execution_timeout"
	}
	var transport *transportFailure
	if errors.As(err, &transport) {
		return "upstream_failed"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "invalid_result"
	}
	return "invalid_result"
}

type transportFailure struct{ cause error }

func (*transportFailure) Error() string   { return "image upstream transport failed" }
func (e *transportFailure) Unwrap() error { return e.cause }

// Transport errors may contain private URLs, certificate names or peer bytes.
// Persist only a fixed category; never serialize the original error string.
func captureTransportFailure(ctx context.Context, err error) {
	reason := "request_failed"
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var network *net.OpError
	var timeout net.Error
	switch {
	case errors.Is(err, context.Canceled):
		reason = "request_cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		reason = "request_timeout"
	case errors.Is(err, egress.ErrRedirectBlocked):
		reason = "redirect_blocked"
	case errors.As(err, &dns):
		reason = "dns_failed"
	case errors.As(err, &certificate):
		reason = "tls_certificate_invalid"
	case errors.As(err, &record):
		reason = "tls_protocol_failed"
	case errors.As(err, &network):
		reason = "connection_failed"
	}
	body, _ := json.Marshal(struct {
		Category          string `json:"category"`
		Reason            string `json:"reason"`
		OriginalBodySaved bool   `json:"original_body_saved"`
	}{"image_transport_failed", reason, false})
	upstreamerror.CaptureEvent(ctx, 0, "application/vnd.nonbiriapi.image-diagnostic+json", body)
}

// A rejected success-shaped response may contain generated images. Store a
// clearly synthetic diagnosis, never any bytes or values from that response.
func (s *Service) captureImageOmission(ctx context.Context, ref observability.DiagnosticRef, response responseData, reason string) {
	switch reason {
	case "unreadable_response", "invalid_response", "invalid_images", "missing_task_receipt":
	default:
		return
	}
	body, _ := json.Marshal(struct {
		Category          string `json:"category"`
		Reason            string `json:"reason"`
		OriginalBodySaved bool   `json:"original_body_saved"`
	}{"image_response_omitted", reason, false})
	scoped := s.config.Diagnostics.ErrorScope(ctx, ref)
	upstreamerror.CaptureEvent(scoped, response.status, "application/vnd.nonbiriapi.image-diagnostic+json", body)
}
