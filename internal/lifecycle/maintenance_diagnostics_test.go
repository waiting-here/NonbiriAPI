package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestFailureDomainPreservesSafeLabelThroughErrorChain(t *testing.T) {
	private := errors.New("synthetic confidential diagnostic")
	err := fmt.Errorf("startup: %w", errors.Join(
		lifecycleStageError{phase: "recovery", domain: "claims", index: 3, err: errors.Join(context.DeadlineExceeded, private)},
		lifecycleStageError{phase: "recovery", domain: "reports", index: 5, err: private},
	))
	if got := FailureDomain(err); got != "claims" {
		t.Fatalf("failure domain = %q, want claims", got)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("safe diagnostics lost the underlying cancellation category")
	}
	for _, cause := range []error{nil, private, context.Canceled} {
		if got := FailureDomain(cause); got != "" {
			t.Fatalf("unclassified error exposed a domain: %q", got)
		}
	}
}
