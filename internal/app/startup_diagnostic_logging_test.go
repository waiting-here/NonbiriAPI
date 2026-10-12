package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestStartupFailureLogOmitsPrivateCause(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	trace := db.NewStartupTrace(time.Now(), nil)
	db.RecordStartupStage(trace.Context(context.Background()), db.StageBusinessRecovery)
	logStartupFailure(trace, errors.Join(context.DeadlineExceeded, errors.New("synthetic confidential diagnostic")), false)
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["category"] != "deadline" || fields["stage"] != "business_recovery" || fields["recovery_domain"] != "" {
		t.Fatalf("unexpected safe failure fields: %v", fields)
	}
	if strings.Contains(output.String(), "synthetic confidential diagnostic") {
		t.Fatal("startup logging exposed the private error cause")
	}
}
