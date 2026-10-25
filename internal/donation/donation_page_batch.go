package donation

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
)

// Only pure numbered donation lists select this pool. Known-ID retained reads
// and every mutation keep their existing writer-backed authorization paths.
func (s *Service) beginDonationPageTx(ctx context.Context, role reviewerRole, userID int64) (*sql.Tx, error) {
	if s == nil || s.readDB == nil {
		return nil, ErrUnavailable
	}
	if role == reviewerSteward {
		tx, _, _, err := s.beginStewardReviewTx(ctx, s.readDB, role, userID, true)
		return tx, err
	}
	tx, _, err := s.beginBrowseActorTxOn(ctx, s.readDB, role, userID)
	return tx, err
}

type donationPageItem struct {
	header   AdminDonation
	summary  DonationSummary
	keyCount int64
}

// ids come from the authorized current page in the same snapshot. Every query
// is restricted to those IDs, including source grouping and window functions.
func readDonationPageSummariesTx(ctx context.Context, tx *sql.Tx, ids []int64, now int64) (map[string]*donationPageItem, error) {
	items := make(map[string]*donationPageItem, len(ids))
	if len(ids) == 0 {
		return items, nil
	}
	if len(ids) > 100 {
		return nil, ErrInvalidRequest
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	timed := func(times int) []any {
		values := make([]any, times, times+len(args))
		for i := range values {
			values[i] = now
		}
		return append(values, args...)
	}

	rows, err := tx.QueryContext(ctx, donationHeaderSelectSQL+" WHERE d.id IN ("+marks+")", timed(1)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		header, err := scanDonationHeader(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items[header.ID] = &donationPageItem{header: header}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) != len(ids) {
		return nil, ErrNotFound
	}

	rows, err = tx.QueryContext(ctx, `SELECT d.id,`+logicallyActiveDonationSQL+`,h.state,h.revision,h.processed_at,h.processed_by_role,h.closed_at,h.closed_reason
FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE d.id IN (`+marks+`)`, timed(1)...)
	if err != nil {
		return nil, err
	}
	handlingCount := 0
	for rows.Next() {
		var id, role, reason string
		var active bool
		var revision int64
		var handling DonationHandling
		if err := rows.Scan(&id, &active, &handling.State, &revision, &handling.ProcessedAt, &role, &handling.ClosedAt, &reason); err != nil {
			rows.Close()
			return nil, err
		}
		item, ok := items[id]
		if !ok {
			rows.Close()
			return nil, ErrInvariant
		}
		item.header.Handling = projectHandling(handling, revision, role, reason)
		if (item.header.Status == "pending" || item.header.Status == "approved") && !active {
			item.header.Status = "expired"
			if item.header.Handling.State == "pending" {
				item.header.Handling.State = "closed"
				expired := "expired"
				item.header.Handling.ClosedReason = &expired
			}
		}
		item.summary = donationSummaryHeader(item.header)
		item.summary.KeyCount, item.summary.SourceCount = "0", "0"
		handlingCount++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if handlingCount != len(items) {
		return nil, ErrInvariant
	}

	rows, err = tx.QueryContext(ctx, `SELECT dk.donation_id,`+donationstate.SQL()+` AS state,COUNT(*)
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id `+donationstate.PhysicalJoins+`
WHERE dk.donation_id IN (`+marks+`) GROUP BY dk.donation_id,state`, timed(4)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state string
		var count int64
		if err := rows.Scan(&id, &state, &count); err != nil {
			rows.Close()
			return nil, err
		}
		item, ok := items[id]
		if !ok {
			rows.Close()
			return nil, ErrInvariant
		}
		if _, ok := item.summary.StateCounts[state]; !ok {
			rows.Close()
			return nil, ErrInvariant
		}
		item.summary.StateCounts[state] = strconv.FormatInt(count, 10)
		item.keyCount += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		item.summary.KeyCount = strconv.FormatInt(item.keyCount, 10)
	}

	rows, err = tx.QueryContext(ctx, `WITH grouped AS (
 SELECT dk.donation_id,`+sourceIdentitySQL+` AS source_key,MAX(dk.id) AS preview
 FROM donation_keys dk WHERE dk.donation_id IN (`+marks+`) GROUP BY dk.donation_id,source_key
), ranked AS (
 SELECT donation_id,source_key,preview,COUNT(*) OVER(PARTITION BY donation_id) AS source_count,
 ROW_NUMBER() OVER(PARTITION BY donation_id ORDER BY source_key) AS position FROM grouped
)
SELECT ranked.donation_id,ranked.source_count,dk.connector_type,dk.canonical_base_url,dk.mainstream_channel_id,dk.mainstream_channel_name
FROM ranked JOIN donation_keys dk ON dk.id=ranked.preview WHERE ranked.position<=3
ORDER BY ranked.donation_id,ranked.source_key`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var count int64
		var source SafeSource
		if err := rows.Scan(&id, &count, &source.ConnectorType, &source.BaseURL, &source.ChannelID, &source.Name); err != nil {
			rows.Close()
			return nil, err
		}
		item, ok := items[id]
		if !ok {
			rows.Close()
			return nil, ErrInvariant
		}
		source.Kind = "custom"
		if source.ChannelID != nil {
			source.Kind = "mainstream"
		}
		item.summary.SourceCount = strconv.FormatInt(count, 10)
		item.summary.Sources = append(item.summary.Sources, source)
	}
	err = rows.Err()
	rows.Close()
	return items, err
}
