package forward

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func randomSafetyIdentifierKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, safetyIdentifierKeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate safety identifier test key: %v", err)
	}
	return key
}

func derivedSafetyIdentifierKey(t *testing.T, vault *secret.Vault) []byte {
	t.Helper()
	key, err := vault.DeriveSubkey([]byte(SafetyIdentifierSubkeyInfo))
	if err != nil {
		t.Fatalf("derive safety identifier test key: %v", err)
	}
	return key
}

func expectedSafetyIdentifier(t *testing.T, vault *secret.Vault, userID int64) string {
	t.Helper()
	key := derivedSafetyIdentifierKey(t, vault)
	generator, err := newSafetyIdentifierGenerator(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer generator.close()
	identifier, err := generator.generate(userID)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func assertSafetyIdentifierFormat(t *testing.T, identifier string) {
	t.Helper()
	if len(identifier) != safetyIdentifierLength {
		t.Fatalf("identifier length=%d want=%d", len(identifier), safetyIdentifierLength)
	}
	if !strings.HasPrefix(identifier, safetyIdentifierPrefix) {
		t.Fatalf("identifier prefix=%q", identifier)
	}
	digestText := strings.TrimPrefix(identifier, safetyIdentifierPrefix)
	if len(digestText) != safetyIdentifierDigestText || strings.ContainsRune(digestText, '=') {
		t.Fatalf("identifier is not canonical unpadded base32: %q", identifier)
	}
	for _, character := range digestText {
		if (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7') {
			continue
		}
		t.Fatalf("identifier contains non-RFC4648 character %q: %q", character, identifier)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(digestText)
	if err != nil || len(decoded) != sha256.Size {
		clear(decoded)
		t.Fatalf("decode identifier digest: len=%d err=%v", len(decoded), err)
	}
	clear(decoded)
}

func TestSafetyIdentifierGeneratorKnownVectorStabilityAndBounds(t *testing.T) {
	key := make([]byte, safetyIdentifierKeyBytes)
	for index := range key {
		key[index] = byte(index)
	}
	generator, err := newSafetyIdentifierGenerator(key)
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	t.Cleanup(func() { _ = generator.close() })

	const expected = "nbu_v2_BSU5XHEZS4BWDEVKDYYB57RKIGHCMSOMFRR7AWF3DFJ4DLIUFTIA"
	first, err := generator.generate(42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.generate(42)
	if err != nil {
		t.Fatal(err)
	}
	if first != expected || second != expected {
		t.Fatalf("known vector mismatch: first=%q second=%q", first, second)
	}
	assertSafetyIdentifierFormat(t, first)

	otherUser, err := generator.generate(43)
	if err != nil {
		t.Fatal(err)
	}
	if otherUser == first {
		t.Fatal("different users received the same safety identifier")
	}
	assertSafetyIdentifierFormat(t, otherUser)

	maximumUser, err := generator.generate(9223372036854775807)
	if err != nil {
		t.Fatal(err)
	}
	assertSafetyIdentifierFormat(t, maximumUser)

	for _, userID := range []int64{0, -1, -9223372036854775807 - 1} {
		if identifier, err := generator.generate(userID); !errors.Is(err, errInvalidSafetyIdentifierID) || identifier != "" {
			t.Fatalf("userID=%d identifier=%q err=%v", userID, identifier, err)
		}
	}
}

func TestSafetyIdentifierGeneratorKeyAndDeploymentSeparation(t *testing.T) {
	firstKey := randomSafetyIdentifierKey(t)
	secondKey := randomSafetyIdentifierKey(t)
	for bytes.Equal(firstKey, secondKey) {
		if _, err := rand.Read(secondKey); err != nil {
			t.Fatal(err)
		}
	}
	first, err := newSafetyIdentifierGenerator(firstKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newSafetyIdentifierGenerator(secondKey)
	if err != nil {
		t.Fatal(err)
	}
	clear(firstKey)
	clear(secondKey)
	t.Cleanup(func() {
		_ = first.close()
		_ = second.close()
	})

	firstID, err := first.generate(731337)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := second.generate(731337)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("different deployment-derived keys produced the same identifier")
	}
}

func TestSafetyIdentifierGeneratorRejectsMissingKeyAndClearsOnClose(t *testing.T) {
	for _, key := range [][]byte{nil, {}, make([]byte, safetyIdentifierKeyBytes-1), make([]byte, safetyIdentifierKeyBytes+1)} {
		if generator, err := newSafetyIdentifierGenerator(key); !errors.Is(err, errInvalidSafetyIdentifierKey) || generator != nil {
			t.Fatalf("key length=%d generator=%v err=%v", len(key), generator, err)
		}
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	generator, err := newSafetyIdentifierGenerator(key)
	if err != nil {
		t.Fatal(err)
	}
	config := ServiceConfig{SafetyIdentifierKey: key}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	logger.Info("format safety identifier state", "generator", generator, "config", config)
	formatted := fmt.Sprintf("%v %+v %#v %v %+v %#v %s", generator, generator, generator, config, config, config, logOutput.String())
	clear(key)
	for _, marker := range []string{"0123456789abcdef", "48 49 50 51", "0x30, 0x31, 0x32"} {
		if strings.Contains(formatted, marker) {
			t.Fatalf("routine formatting exposed key material: %q", formatted)
		}
	}
	if err := generator.close(); err != nil {
		t.Fatal(err)
	}
	if err := generator.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if identifier, err := generator.generate(1); !errors.Is(err, errSafetyIdentifierClosed) || identifier != "" {
		t.Fatalf("generate after close identifier=%q err=%v", identifier, err)
	}
	if !generator.closed || !bytes.Equal(generator.key[:], make([]byte, safetyIdentifierKeyBytes)) {
		t.Fatal("close did not clear retained key material")
	}
}

func TestSafetyIdentifierGeneratorConcurrentGenerationAndClose(t *testing.T) {
	key := randomSafetyIdentifierKey(t)
	generator, err := newSafetyIdentifierGenerator(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	want, err := generator.generate(919191)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 64
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reportedReady := false
			for {
				identifier, generateErr := generator.generate(919191)
				if !reportedReady {
					ready <- struct{}{}
					reportedReady = true
				}
				switch {
				case generateErr == nil && identifier != want:
					errs <- errors.New("concurrent generation changed identifier")
					return
				case generateErr == nil:
				case errors.Is(generateErr, errSafetyIdentifierClosed) && identifier == "":
					return
				default:
					errs <- fmt.Errorf("unexpected concurrent generation result: identifier=%q err=%v", identifier, generateErr)
					return
				}
			}
		}()
	}
	close(start)
	for worker := 0; worker < workers; worker++ {
		<-ready
	}
	if err := generator.close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if identifier, err := generator.generate(919191); !errors.Is(err, errSafetyIdentifierClosed) || identifier != "" {
		t.Fatalf("post-close generation identifier=%q err=%v", identifier, err)
	}
}

func TestSafetyIdentifierNoKeyMillionCandidateEnumeration(t *testing.T) {
	key := randomSafetyIdentifierKey(t)
	generator, err := newSafetyIdentifierGenerator(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer generator.close()

	const targetUserID int64 = 731337
	target, err := generator.generate(targetUserID)
	if err != nil {
		t.Fatal(err)
	}
	targetDigest, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimPrefix(target, safetyIdentifierPrefix))
	if err != nil || len(targetDigest) != sha256.Size {
		clear(targetDigest)
		t.Fatalf("decode target: len=%d err=%v", len(targetDigest), err)
	}
	defer clear(targetDigest)

	// This is the strongest public-algorithm regression available to an
	// attacker without the deployment key: hash the exact public purpose and
	// every positive fixed-width candidate. None can verify the keyed target.
	for candidate := int64(1); candidate <= 1_000_000; candidate++ {
		message := safetyIdentifierMessage(candidate)
		publicDigest := sha256.Sum256(message[:])
		if bytes.Equal(publicDigest[:], targetDigest) {
			clear(message[:])
			clear(publicDigest[:])
			t.Fatalf("public candidate enumeration verified user %d", candidate)
		}
		clear(message[:])
		clear(publicDigest[:])
	}
}

type safetyIdentifierRepository struct {
	resolveCalls atomic.Int32
	route        db.ForwardRoute
}

func (r *safetyIdentifierRepository) ListCallerModels(context.Context, int64, int) ([]db.CallerModel, error) {
	return nil, nil
}

func (r *safetyIdentifierRepository) ResolveForwardRoute(context.Context, int64, string, int) (db.ForwardRoute, error) {
	r.resolveCalls.Add(1)
	return r.route, nil
}

type safetyIdentifierRunner struct {
	runCalls atomic.Int32
	input    chan AttemptInput
	release  <-chan struct{}
}

func (r *safetyIdentifierRunner) Run(ctx context.Context, _ http.ResponseWriter, input AttemptInput) openai.AttemptResult {
	r.runCalls.Add(1)
	if r.input != nil {
		r.input <- input
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return canceledAttemptResult()
		}
	}
	return openai.AttemptResult{Success: true, Committed: true}
}

func newSafetyIdentifierServiceForTest(t *testing.T, repository RouteRepository, runner AttemptRunner) *Service {
	t.Helper()
	key := randomSafetyIdentifierKey(t)
	service, err := NewService(ServiceConfig{
		Repository:          repository,
		Runner:              runner,
		SafetyIdentifierKey: key,
	})
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestForwardServiceRequiresLiveSafetyIdentifierGenerator(t *testing.T) {
	repository := &safetyIdentifierRepository{}
	runner := &safetyIdentifierRunner{}
	for _, key := range [][]byte{nil, make([]byte, safetyIdentifierKeyBytes-1), make([]byte, safetyIdentifierKeyBytes+1)} {
		service, err := NewService(ServiceConfig{Repository: repository, Runner: runner, SafetyIdentifierKey: key})
		if !errors.Is(err, errInvalidSafetyIdentifierKey) || service != nil {
			t.Fatalf("key length=%d service=%v err=%v", len(key), service, err)
		}
	}

	service := newSafetyIdentifierServiceForTest(t, repository, runner)
	if err := service.safetyIdentifiers.close(); err != nil {
		t.Fatal(err)
	}
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), 1, &openai.ChatRequest{Model: "p/m"})
	if !errors.Is(err, ErrInternal) || result != (openai.AttemptResult{}) {
		t.Fatalf("closed generator result=%+v err=%v", result, err)
	}
	if repository.resolveCalls.Load() != 0 || runner.runCalls.Load() != 0 {
		t.Fatalf("closed generator reached repository/runner: resolves=%d runs=%d", repository.resolveCalls.Load(), runner.runCalls.Load())
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardServiceCloseWaitsForInflightAndThenFailsClosed(t *testing.T) {
	const userID int64 = 77
	repository := &safetyIdentifierRepository{route: db.ForwardRoute{
		ModelID: 1, UserID: userID, FullName: "p/m", RouteStrategy: "ordered",
		Candidates: []db.ForwardCandidate{{
			BindingID: 1, ModelID: 1, EndpointID: 1, EndpointKeyID: 1, UpstreamModelID: "upstream/model",
		}},
	}}
	entered := make(chan AttemptInput, 1)
	release := make(chan struct{})
	runner := &safetyIdentifierRunner{input: entered, release: release}
	service := newSafetyIdentifierServiceForTest(t, repository, runner)

	forwardDone := make(chan error, 1)
	go func() {
		_, forwardErr := service.Forward(context.Background(), httptest.NewRecorder(), userID, &openai.ChatRequest{Model: "p/m"})
		forwardDone <- forwardErr
	}()
	input := <-entered
	assertSafetyIdentifierFormat(t, input.SafetyIdentifier)
	if input.UserID != userID || strings.HasPrefix(input.SafetyIdentifier, "nbu_") && !strings.HasPrefix(input.SafetyIdentifier, safetyIdentifierPrefix) {
		t.Fatalf("runner input=%+v", input)
	}

	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- service.Close()
	}()
	<-closeStarted
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight request ended: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-forwardDone; err != nil {
		t.Fatalf("in-flight forward: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !service.closed || !service.safetyIdentifiers.closed || !bytes.Equal(service.safetyIdentifiers.key[:], make([]byte, safetyIdentifierKeyBytes)) {
		t.Fatal("service close did not clear retained safety identifier state")
	}

	beforeRuns := runner.runCalls.Load()
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), userID, &openai.ChatRequest{Model: "p/m"})
	if !errors.Is(err, ErrInternal) || result != (openai.AttemptResult{}) || runner.runCalls.Load() != beforeRuns {
		t.Fatalf("post-close result=%+v err=%v runs=%d want=%d", result, err, runner.runCalls.Load(), beforeRuns)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
