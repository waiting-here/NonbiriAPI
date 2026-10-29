package logapi

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/textprivacy"
)

type attemptPrivacySource struct {
	sequence   int
	baseURL    string
	mainstream bool
}

// Both branches are restricted to already authorized log roots. Completed
// attempts retain their own endpoint snapshot; an unfinished attempt can use
// only its exact active claim's endpoint metadata, never another attempt's URL.
const completedPrivacySources = `SELECT a.request_log_id,a.attempt_seq,a.canonical_base_url,
 EXISTS(SELECT 1 FROM mainstream_channels m JOIN mainstream_channel_entries ce ON ce.channel_id=m.id
 WHERE m.state='active' AND m.enabled=1 AND ce.enabled=1 AND ce.canonical_base_url=a.canonical_base_url)
 FROM request_attempts a
 WHERE a.request_log_id IN (`

const activePrivacySources = `SELECT l.id,c.attempt_seq,COALESCE(t.canonical_base_url,s.canonical_base_url),
 EXISTS(SELECT 1 FROM mainstream_channels m JOIN mainstream_channel_entries ce ON ce.channel_id=m.id
 WHERE m.state='active' AND m.enabled=1 AND ce.enabled=1 AND ce.canonical_base_url=COALESCE(t.canonical_base_url,s.canonical_base_url))
 FROM request_logs l JOIN dispatch_claims c ON c.logical_request_id=l.logical_request_id AND c.state='dispatched'
 LEFT JOIN dispatch_claim_targets t ON t.claim_id=c.id
 LEFT JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
 WHERE NOT EXISTS(SELECT 1 FROM request_attempts a WHERE a.request_log_id=l.id AND a.attempt_seq=c.attempt_seq)
 AND l.id IN (`

func readLogPrivacySources(ctx context.Context, reader logReadQueryer, ids []int64, attempt int) (map[int64][]attemptPrivacySource, error) {
	out := make(map[int64][]attemptPrivacySource, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > maximumLimit || attempt < 0 || attempt > 100 {
		return nil, ErrInvalid
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, 2*len(ids)+3)
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalid
		}
		args = append(args, id)
	}
	query := completedPrivacySources + placeholders + ")"
	if attempt != 0 {
		query += " AND a.attempt_seq=?"
		args = append(args, attempt)
	}
	query += " UNION ALL " + activePrivacySources + placeholders + ")"
	for _, id := range ids {
		args = append(args, id)
	}
	if attempt != 0 {
		query += " AND c.attempt_seq=?"
		args = append(args, attempt)
	}
	query += " ORDER BY 1,2 LIMIT ?"
	args = append(args, len(ids)*100+1)
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, translateReadError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var source attemptPrivacySource
		if err := rows.Scan(&id, &source.sequence, &source.baseURL, &source.mainstream); err != nil {
			return nil, translateReadError(ctx, err)
		}
		if source.sequence < 1 || source.sequence > 100 || !validBaseURL(source.baseURL) || len(out[id]) >= 100 {
			return nil, ErrInvariant
		}
		out[id] = append(out[id], source)
	}
	if err := rows.Err(); err != nil {
		return nil, translateReadError(ctx, err)
	}
	return out, nil
}

func requestDNSRedactor(sources []attemptPrivacySource) *textprivacy.DNSRedactor {
	bases := make([]string, 0, len(sources))
	for _, source := range sources {
		if !source.mainstream {
			bases = append(bases, source.baseURL)
		}
	}
	return textprivacy.NewDNSRedactor(bases...)
}

func attemptDNSRedactor(sources []attemptPrivacySource, sequence string, base string) *textprivacy.DNSRedactor {
	for _, source := range sources {
		if strconv.Itoa(source.sequence) == sequence {
			if source.mainstream {
				return nil
			}
			return textprivacy.NewDNSRedactor(source.baseURL)
		}
	}
	return textprivacy.NewDNSRedactor(base)
}

func redactDNSPointer(value *string, redactor *textprivacy.DNSRedactor) *string {
	if value == nil || redactor == nil {
		return value
	}
	projected := redactor.Redact(*value)
	return &projected
}

func redactUserLogRow(row UserLogRow, redactor *textprivacy.DNSRedactor) UserLogRow {
	switch value := row.(type) {
	case UserSelfLogRow:
		value.Model = redactor.Redact(value.Model)
		value.CallerErrorCode = redactDNSPointer(value.CallerErrorCode, redactor)
		value.RequestPath = redactDNSPointer(value.RequestPath, redactor)
		return value
	case UserCharityLogRow:
		value.Model = redactor.Redact(value.Model)
		value.CallerErrorCode = redactDNSPointer(value.CallerErrorCode, redactor)
		value.RequestPath = redactDNSPointer(value.RequestPath, redactor)
		return value
	}
	return row
}

// Batches are bounded independently of the export size. The query loads only
// endpoint identity and current channel matches, not error payloads.
func redactUserLogRows(ctx context.Context, reader logReadQueryer, ids []int64, rows []UserLogRow) error {
	if len(ids) != len(rows) {
		return ErrInvariant
	}
	for start := 0; start < len(ids); start += maximumLimit {
		end := min(start+maximumLimit, len(ids))
		sources, err := readLogPrivacySources(ctx, reader, ids[start:end], 0)
		if err != nil {
			return err
		}
		for i := start; i < end; i++ {
			rows[i] = redactUserLogRow(rows[i], requestDNSRedactor(sources[ids[i]]))
		}
	}
	return nil
}

func redactUserLogAttempts(attempts []UserSelfLogAttempt, sources []attemptPrivacySource) {
	for i := range attempts {
		value := &attempts[i]
		redactor := attemptDNSRedactor(sources, value.AttemptSeq, value.EndpointBaseURL)
		value.EndpointBaseURL = redactor.Redact(value.EndpointBaseURL)
		value.EndpointNote = redactor.Redact(value.EndpointNote)
		value.KeyNote = redactor.Redact(value.KeyNote)
		value.UpstreamModelID = redactor.Redact(value.UpstreamModelID)
		value.UpstreamCode = redactDNSPointer(value.UpstreamCode, redactor)
		value.Diag = redactDNSPointer(value.Diag, redactor)
		redactFailure(value.ErrorDiagnostics, redactor.Redact)
	}
}

// Saved byte counts and other metadata continue to describe the original
// material, just as they do for IPv4 projection. Only the displayed payload
// changes; a body without provable endpoint identity is concealed in full.
func redactErrorDNS(body observability.ErrorBody, sources []attemptPrivacySource) (observability.ErrorBody, error) {
	project := func(value string) string {
		if len(sources) == 0 {
			return "[redacted]"
		}
		return requestDNSRedactor(sources).Redact(value)
	}
	switch body.Encoding {
	case "utf-8":
		body.Body = project(body.Body)
	case "base64":
		raw, err := base64.StdEncoding.DecodeString(body.Body)
		if err != nil {
			return observability.ErrorBody{}, ErrUnavailable
		}
		defer clear(raw)
		body.Body = base64.StdEncoding.EncodeToString([]byte(project(string(raw))))
	default:
		return observability.ErrorBody{}, ErrUnavailable
	}
	return body, nil
}
