package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type observerExecutionDriver struct{ observerEntered <-chan struct{} }

func (observerExecutionDriver) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeOpenAICompatible
}

type cancelExecutionDriver struct{ entered chan<- struct{} }

func (cancelExecutionDriver) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeOpenAICompatible
}

func (d cancelExecutionDriver) Attempt(ctx context.Context, _ http.ResponseWriter, _ openai.Target, _ *openai.ChatRequest, _ string) openai.AttemptResult {
	close(d.entered)
	<-ctx.Done()
	return openai.AttemptResult{Failure: openai.FailureCanceled, Diagnostic: "request canceled"}
}

func (d observerExecutionDriver) Attempt(_ context.Context, writer http.ResponseWriter, _ openai.Target, _ *openai.ChatRequest, _ string) openai.AttemptResult {
	select {
	case <-d.observerEntered:
	case <-time.After(time.Second):
		return openai.AttemptResult{Failure: openai.FailureInternal, Diagnostic: "observer worker did not start"}
	}
	writer.Header().Set("X-Observer-Test", "preserved")
	writer.WriteHeader(http.StatusAccepted)
	_, _ = writer.Write([]byte("exact-response"))
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return openai.AttemptResult{Success: true, Committed: true, Usage: openai.Usage{Present: true, OutputTokens: 7}}
}

func TestSlowObserverDoesNotChangeAttemptBytesOrCompletion(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	observer, err := NewSafeObserver(1, func(Observation) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &openAIConnector{driver: observerExecutionDriver{observerEntered: entered}}
	recorder := httptest.NewRecorder()
	request := &openai.ChatRequest{Model: "public/model"}
	plaintext := []byte("secret")
	ciphertext := []byte("cipher")
	resultCh := make(chan connectorcontract.AttemptResult, 1)
	go func() {
		resultCh <- connector.Attempt(context.Background(), AttemptInput{
			Target:     connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example/v1", "upstream/model"),
			Credential: connectorcontract.NewShortLivedSecret(plaintext, ciphertext),
			Ingress:    request,
			Policy:     connectorcontract.AttemptPolicy{SafetyIdentifier: "safe_id"},
			Sink:       recorder,
			Observer:   observer,
		})
	}()
	var result connectorcontract.AttemptResult
	select {
	case result = <-resultCh:
	case <-time.After(250 * time.Millisecond):
		close(release)
		_ = observer.Close()
		t.Fatal("slow observer blocked the real attempt")
	}
	if !result.Success || !result.Committed || result.Usage.OutputTokens != 7 {
		t.Fatalf("attempt result changed by observer: %+v", result)
	}
	for _, value := range append(plaintext, ciphertext...) {
		if value != 0 {
			t.Fatal("connector did not clear transferred credential aliases")
		}
	}
	if got := recorder.Body.String(); got != "exact-response" {
		t.Fatalf("caller bytes = %q", got)
	}
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("X-Observer-Test") != "preserved" || !recorder.Flushed {
		t.Fatalf("observer changed status/header/flush: code=%d header=%q flushed=%v", recorder.Code, recorder.Header().Get("X-Observer-Test"), recorder.Flushed)
	}
	close(release)
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSlowObserverDoesNotDelayAttemptCancellation(t *testing.T) {
	observerEntered := make(chan struct{})
	releaseObserver := make(chan struct{})
	observer, err := NewSafeObserver(1, func(Observation) {
		select {
		case <-observerEntered:
		default:
			close(observerEntered)
		}
		<-releaseObserver
	})
	if err != nil {
		t.Fatal(err)
	}
	driverEntered := make(chan struct{})
	protocol := &openAIConnector{driver: cancelExecutionDriver{entered: driverEntered}}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan connectorcontract.AttemptResult, 1)
	go func() {
		resultCh <- protocol.Attempt(ctx, AttemptInput{
			Target:     connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example/v1", "upstream/model"),
			Credential: connectorcontract.NewShortLivedSecret([]byte("secret"), []byte("cipher")),
			Ingress:    &openai.ChatRequest{Model: "public/model"},
			Policy:     connectorcontract.AttemptPolicy{SafetyIdentifier: "safe_id"},
			Sink:       httptest.NewRecorder(),
			Observer:   observer,
		})
	}()
	select {
	case <-driverEntered:
	case <-time.After(time.Second):
		close(releaseObserver)
		_ = observer.Close()
		t.Fatal("driver did not start")
	}
	select {
	case <-observerEntered:
	case <-time.After(time.Second):
		close(releaseObserver)
		_ = observer.Close()
		t.Fatal("observer handler did not become slow")
	}
	cancel()
	select {
	case result := <-resultCh:
		if result.Failure != connectorcontract.FailureCanceled || result.Committed {
			t.Fatalf("cancellation result changed: %+v", result)
		}
	case <-time.After(250 * time.Millisecond):
		close(releaseObserver)
		_ = observer.Close()
		t.Fatal("slow observer delayed cancellation")
	}
	close(releaseObserver)
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}
