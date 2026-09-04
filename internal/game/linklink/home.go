package linklink

import (
	"context"
	"database/sql"
)

type HomeContinue struct {
	ResourceID string
	State      string
}

type HomeSummary struct {
	Continue []HomeContinue
}

type HomeSummaryInput struct {
	UserID int64
}

// HomeSummaryTx reads only a live LinkLink continuation. It deliberately does
// not catch up an expired deadline or inspect retained terminal summaries.
func (service *Service) HomeSummaryTx(ctx context.Context, tx *sql.Tx, input HomeSummaryInput) (HomeSummary, error) {
	if service == nil || ctx == nil || tx == nil || input.UserID <= 0 {
		return HomeSummary{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return HomeSummary{}, ErrClosed
	}
	now, err := service.decisionNow()
	if err != nil {
		return HomeSummary{}, err
	}
	result := HomeSummary{Continue: []HomeContinue{}}
	record, found, err := loadSessionByUser(ctx, tx, input.UserID)
	if err != nil {
		return HomeSummary{}, err
	}
	if found && now < record.Deadline {
		result.Continue = append(result.Continue, HomeContinue{
			ResourceID: record.ID,
			State:      record.State,
		})
	}
	if len(result.Continue) > 1 || len(result.Continue) == 1 && result.Continue[0].State != "active" {
		return HomeSummary{}, ErrInvariant
	}
	return result, nil
}
