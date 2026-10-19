package headerfacts

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type inboundKey struct{}
type responseKey struct{}
type collector struct {
	mu              sync.Mutex
	requestID       string
	attempt         int
	facts           []Fact
	state           string
	decodedBytes    int
	compressedBytes int
}

func (*collector) String() string   { return "[private header collector]" }
func (*collector) GoString() string { return "[private header collector]" }

// WithRequest runs before adaptation. Request.Host is promoted by net/http;
// no Host is synthesized when it was not supplied.
func WithRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, inboundKey{}, Capture(promoted(r.Header, r.TransferEncoding, r.Trailer), r.Host, 0, time.Now()))
}
func Request(ctx context.Context) Fact {
	fact, _ := ctx.Value(inboundKey{}).(Fact)
	return fact
}
func WithResponse(ctx context.Context, requestID string, attempt int) context.Context {
	return context.WithValue(ctx, responseKey{}, &collector{requestID: requestID, attempt: attempt})
}
func CapturingResponse(ctx context.Context) bool {
	value, _ := ctx.Value(responseKey{}).(*collector)
	return value != nil
}

// CaptureResponse freezes every final response in arrival order. Compression
// is bounded and performed before any completion transaction is submitted.
func CaptureResponse(ctx context.Context, response *http.Response) {
	value, _ := ctx.Value(responseKey{}).(*collector)
	if value == nil || response == nil || response.StatusCode < 100 || response.StatusCode > 999 {
		return
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.state != "" {
		return
	}
	if len(value.facts) >= MaxResponses {
		value.state = "too_large"
		value.facts = nil
		return
	}
	fact := Capture(promoted(response.Header, response.TransferEncoding, response.Trailer), "", response.StatusCode, time.Now())
	if fact.State() != "saved" {
		value.state = fact.State()
		value.facts = nil
		return
	}
	value.decodedBytes += fact.decodedBytes
	value.compressedBytes += len(fact.data) + 32
	if value.decodedBytes > MaxCollectionBytes || value.compressedBytes > MaxCollectionBytes {
		value.state = "too_large"
		value.facts = nil
		return
	}
	value.facts = append(value.facts, fact)
}

type preparedKey struct{}
type prepared struct {
	requestID string
	attempt   int
	fact      Fact
}

// PrepareResponse seals the collection outside the write transaction. A
// request/attempt identity check prevents context reuse from crossing claims.
func PrepareResponse(ctx context.Context, requestID string, attempt int) context.Context {
	value, _ := ctx.Value(responseKey{}).(*collector)
	if value == nil || value.requestID != requestID || value.attempt != attempt {
		return ctx
	}
	value.mu.Lock()
	fact := encodeCollection(value.facts, value.state)
	value.mu.Unlock()
	return context.WithValue(ctx, preparedKey{}, prepared{requestID, attempt, fact})
}
func Response(ctx context.Context, requestID string, attempt int) Fact {
	value, _ := ctx.Value(preparedKey{}).(prepared)
	if value.requestID != requestID || value.attempt != attempt {
		return Fact{}
	}
	return value.fact
}

// promoted retains values net/http moves out of Header. Trailer declarations
// describe names only: body trailers have not arrived at header capture time.
func promoted(headers http.Header, transfer []string, trailer http.Header) http.Header {
	if len(transfer) == 0 && len(trailer) == 0 {
		return headers
	}
	copy := headers.Clone()
	if copy == nil {
		copy = make(http.Header)
	}
	if len(transfer) > 0 && copy.Get("Transfer-Encoding") == "" {
		for _, value := range transfer {
			copy.Add("Transfer-Encoding", value)
		}
	}
	if len(trailer) > 0 && copy.Get("Trailer") == "" {
		names := make([]string, 0, len(trailer))
		for name := range trailer {
			names = append(names, name)
		}
		sort.Strings(names)
		copy.Set("Trailer", strings.Join(names, ", "))
	}
	return copy
}
