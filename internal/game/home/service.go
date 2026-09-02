// Package home serves the derived, non-persistent game summary used by the
// authenticated user home page.
package home

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var (
	ErrInvalid       = errors.New("game home: invalid request")
	ErrUnauthorized  = errors.New("game home: unauthorized")
	ErrForbidden     = errors.New("game home: forbidden")
	ErrMaintenance   = errors.New("game home: maintenance")
	ErrResourceLimit = errors.New("game home: resource limit")
	ErrUnavailable   = errors.New("game home: unavailable")
	ErrInvariant     = errors.New("game home: invariant violation")
)

type ContinueItem struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
	RouteID    string `json:"route_id"`
}

type PendingResult struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	CreatedAt  int64  `json:"created_at"`
	RouteID    string `json:"route_id"`
}

type Summary struct {
	Continue       []ContinueItem  `json:"continue"`
	PendingResults []PendingResult `json:"pending_results"`
}

type LinkLinkSource interface {
	HomeSummaryTx(context.Context, *sql.Tx, linklink.HomeSummaryInput) (linklink.HomeSummary, error)
}

type RPSSource interface {
	HomeSummaryTx(context.Context, *sql.Tx, int64) (rps.HomeSummary, error)
}

type Options struct {
	Database       *sql.DB
	UserAuthorizer resources.FinalTxAuthorizer
	LinkLink       LinkLinkSource
	RPS            RPSSource
}

type Service struct {
	database       *sql.DB
	userAuthorizer resources.FinalTxAuthorizer
	linklink       LinkLinkSource
	rps            RPSSource
}

func New(options Options) (*Service, error) {
	if options.Database == nil || options.UserAuthorizer == nil || options.LinkLink == nil || options.RPS == nil {
		return nil, ErrInvalid
	}
	return &Service{
		database: options.Database, userAuthorizer: options.UserAuthorizer,
		linklink: options.LinkLink, rps: options.RPS,
	}, nil
}

// Read obtains all three domain projections from one read transaction. The
// transaction is rolled back after projection so this endpoint cannot ACK or
// otherwise persist an observation.
func (service *Service) Read(ctx context.Context, userID int64) (Summary, error) {
	if service == nil || service.database == nil || ctx == nil || userID <= 0 {
		return Summary{}, ErrInvalid
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Summary{}, classifyError(err)
	}
	defer tx.Rollback()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return Summary{}, classifyAuthorization(err)
	}

	result := Summary{Continue: []ContinueItem{}, PendingResults: []PendingResult{}}
	fishingSummary, err := fishing.HomeSummaryTx(ctx, tx, userID)
	if err != nil {
		return Summary{}, classifyProviderError(err)
	}
	for _, item := range fishingSummary.Continue {
		if item.State != fishing.HomeStateSettlementPending && item.State != fishing.HomeStateRecoveryRequired ||
			!db.ValidateOpaqueID(item.ResourceID, "fb_") {
			return Summary{}, ErrInvariant
		}
		result.Continue = append(result.Continue, ContinueItem{
			Game: game.FishingID, ResourceID: item.ResourceID, State: item.State, RouteID: "game-fishing",
		})
	}
	for _, item := range fishingSummary.PendingResults {
		if !db.ValidateOpaqueID(item.ResourceID, "fb_") || !validTime(item.CreatedAt) {
			return Summary{}, ErrInvariant
		}
		result.PendingResults = append(result.PendingResults, PendingResult{
			Game: game.FishingID, ResourceID: item.ResourceID, CreatedAt: item.CreatedAt, RouteID: "game-fishing",
		})
	}

	linkSummary, err := service.linklink.HomeSummaryTx(ctx, tx, linklink.HomeSummaryInput{UserID: userID})
	if err != nil {
		return Summary{}, classifyProviderError(err)
	}
	if len(linkSummary.Continue) > 1 {
		return Summary{}, ErrInvariant
	}
	for _, item := range linkSummary.Continue {
		if item.State != "active" || !db.ValidateOpaqueID(item.ResourceID, "ll_") {
			return Summary{}, ErrInvariant
		}
		result.Continue = append(result.Continue, ContinueItem{
			Game: game.LinkLinkID, ResourceID: item.ResourceID, State: item.State, RouteID: "game-linklink",
		})
	}

	rpsSummary, err := service.rps.HomeSummaryTx(ctx, tx, userID)
	if err != nil {
		return Summary{}, classifyProviderError(err)
	}
	if len(rpsSummary.Continue) > 1 || len(rpsSummary.PendingResults) > 1 ||
		len(rpsSummary.Continue) != 0 && len(rpsSummary.PendingResults) != 0 {
		return Summary{}, ErrInvariant
	}
	for _, item := range rpsSummary.Continue {
		if item.Game != game.RPSID || item.RouteID != "game-rps" ||
			item.State != rps.StateStarted && item.State != rps.StateTerminalProcessing ||
			!db.ValidateOpaqueID(item.ResourceID, "rps_") {
			return Summary{}, ErrInvariant
		}
		result.Continue = append(result.Continue, ContinueItem(item))
	}
	for _, item := range rpsSummary.PendingResults {
		if item.Game != game.RPSID || item.RouteID != "game-rps" ||
			!db.ValidateOpaqueID(item.ResourceID, "rps_") || !validTime(item.CreatedAt) {
			return Summary{}, ErrInvariant
		}
		result.PendingResults = append(result.PendingResults, PendingResult(item))
	}
	if len(result.PendingResults) > 100 {
		return Summary{}, ErrResourceLimit
	}

	if len(result.Continue) > 3 {
		return Summary{}, ErrInvariant
	}
	sort.SliceStable(result.Continue, func(left, right int) bool {
		return gameOrder(result.Continue[left].Game) < gameOrder(result.Continue[right].Game)
	})
	sort.Slice(result.PendingResults, func(left, right int) bool {
		first, second := result.PendingResults[left], result.PendingResults[right]
		if first.CreatedAt != second.CreatedAt {
			return first.CreatedAt < second.CreatedAt
		}
		if gameOrder(first.Game) != gameOrder(second.Game) {
			return gameOrder(first.Game) < gameOrder(second.Game)
		}
		return first.ResourceID < second.ResourceID
	})
	return result, nil
}

func gameOrder(gameID string) int {
	switch gameID {
	case game.FishingID:
		return 0
	case game.LinkLinkID:
		return 1
	case game.RPSID:
		return 2
	default:
		return 3
	}
}

func validTime(value int64) bool { return value >= 0 && value <= 253402300799 }

func classifyAuthorization(err error) error {
	switch {
	case errors.Is(err, resources.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, resources.ErrMaintenance):
		return ErrMaintenance
	default:
		return classifyError(err)
	}
}

func classifyProviderError(err error) error {
	switch {
	case errors.Is(err, fishing.ErrHomeUnavailable), errors.Is(err, linklink.ErrServiceUnavailable), errors.Is(err, rps.ErrServiceUnavailable),
		errors.Is(err, linklink.ErrClosed), errors.Is(err, rps.ErrClosed):
		return ErrUnavailable
	case errors.Is(err, fishing.ErrHomeResourceLimit):
		return ErrResourceLimit
	case errors.Is(err, fishing.ErrHomeInvalid), errors.Is(err, fishing.ErrHomeInvariant),
		errors.Is(err, linklink.ErrInvalidRequest), errors.Is(err, linklink.ErrInvariant),
		errors.Is(err, rps.ErrInvalidRequest), errors.Is(err, rps.ErrInvariant):
		return ErrInvariant
	default:
		return classifyError(err)
	}
}

func classifyError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "database is locked") || strings.Contains(message, "database is busy") || strings.Contains(message, "sqlite_busy") {
		return ErrUnavailable
	}
	return err
}
