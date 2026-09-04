package activities

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
)

// CursorKeyDeriver supplies the generation-scoped pagination key. It is kept
// behind the same narrow interface as the other Generation 2 repositories.
type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// UserFinalTxAuthorizer revalidates the request-bound user identity in every
// write transaction before idempotency replay is consulted.
type UserFinalTxAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}

// AdminFinalAuthorizer revalidates the request-bound live administrator role
// inside the final domain transaction for both reads and mutations.
type AdminFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

// UserMutationGate linearizes maintenance and any other activity admission
// overlay with the business write. A completed settlement does not use this
// gate because it continues persisted system authority.
type UserMutationGate interface {
	AuthorizeUserActivity(context.Context, *sql.Tx, int64) error
}

type UserPrincipal struct {
	UserID int64
}

type AdminPrincipal struct {
	UserID int64
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)

// UserRouteRegistrar and AdminRouteRegistrar are entry authorization and
// station/maintenance seams. Root composition adapts the central auth runtime
// without making this domain depend on request-context internals.
type UserRouteRegistrar interface {
	RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error
}

type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error
}

// AccountEventSink is implemented by accountstream.Hub.
type AccountEventSink interface {
	PrepareActivitiesPublish(bool) (accountstream.ActivitiesPublishPlan, error)
	PublishActivitiesCommitted(context.Context, int64, accountstream.ActivitiesPublishPlan, accountstream.PublishedEvent) (accountstream.Frame, error)
}

// PostCommitPublisher consumes only safe commit facts. Implementations must
// rebuild complete per-account projections after the domain commit.
type PostCommitPublisher interface {
	Publish(context.Context, PublishFacts) error
}

// PublishErrorReporter is an observability seam. A publication failure is
// reported after commit and can never change the successful business result.
type PublishErrorReporter interface {
	ReportActivitiesPublishError(error)
}
