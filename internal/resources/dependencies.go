package resources

import (
	"context"
	"database/sql"
	"net/http"
)

type BaseURLValidator interface {
	ValidateBaseURL(string) (string, error)
}

type SecretWriteInput struct {
	CanonicalBaseURL string
	ConnectorType    string
	Plaintext        []byte
	CreatedAt        int64
}

type StoredSecret struct {
	RefID       int64
	Fingerprint [32]byte
	DisplayHead string
	DisplayTail string
}

// SecretWriter is implemented by the credential/claim owner. The resource
// repository transfers plaintext once and never reads a persisted secret.
type SecretWriter interface {
	WriteEndpointSecret(context.Context, *sql.Tx, SecretWriteInput) (StoredSecret, error)
	MarkEndpointSecretOrphaned(context.Context, *sql.Tx, int64, int64) error
}

// EndpointKeyDeletionHook lets the donation owner terminate references before
// the resource repository physically deletes verified owner keys. The hook
// runs in the caller's transaction and must not independently authorize or commit.
type EndpointKeyDeletionHook interface {
	PrepareEndpointKeyDeletion(context.Context, *sql.Tx, int64, []int64, int64) error
}

// DiscoveryClaimRail owns claim, secret access, dispatch ordering, and the
// actual connector call. Its result contains only bounded catalog facts.
type DiscoveryClaimRail interface {
	Discover(context.Context, DiscoveryClaimInput) (DiscoveryClaimResult, error)
}

// DiscoveryWorker provides non-blocking, bounded admission for accepted
// discovery continuations. A successful reservation owns one admission slot
// until Start's callback returns or Release is called. Start supplies a
// bounded context detached from the HTTP request and canceled by worker
// shutdown.
type DiscoveryWorker interface {
	ReserveDiscovery() (DiscoveryReservation, bool)
}

type DiscoveryReservation interface {
	Start(func(context.Context))
	Release()
}

type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// FinalTxAuthorizer revalidates the request-bound user identity and live
// account state inside the repository's write transaction. Implementations
// may recover request identity from ctx; the repository never caches an
// authorization result and invokes this hook before idempotency replay.
type FinalTxAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}

type UserPrincipal struct {
	UserID int64
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)

// UserRouteRegistrar is the authorization/maintenance seam. Implementations
// authenticate before invoking a handler, including every replay attempt.
type UserRouteRegistrar interface {
	RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error
}
