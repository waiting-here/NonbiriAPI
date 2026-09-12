package donation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Clone the complete synthetic rows with explicit identity replacements. No
// fixture drops constraints or creates a donation without its physical member.
func seedBrowseScale(t *testing.T, e *donationTestEnv, owner int64, count int, distinct bool) string {
	t.Helper()
	d, key := createBrowseDonation(t, e, owner, 'z', "https://source-00000.example.test/v1")
	id, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	var endpoint, secret int64
	if err := e.store.DB().QueryRow(`SELECT endpoint_id,secret_ref_id FROM endpoint_keys WHERE id=?`, key).Scan(&endpoint, &secret); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	url := `'https://source-00000.example.test/v1'`
	if distinct {
		url = `printf('https://source-%05d.example.test/v1',seq.n)`
	}
	shift := func(base int64) string { return fmt.Sprintf("%d+seq.n", base) }
	for _, table := range []struct {
		name, where string
		replace     map[string]string
	}{
		{"endpoints", fmt.Sprintf("id=%d", endpoint), map[string]string{"id": shift(endpoint), "base_url": url}},
		{"endpoint_key_secrets", fmt.Sprintf("id=%d", secret), map[string]string{"id": shift(secret), "context_id": "randomblob(16)", "canonical_base_url": url}},
		{"endpoint_keys", fmt.Sprintf("id=%d", key), map[string]string{"id": shift(key), "endpoint_id": shift(endpoint), "secret_ref_id": shift(secret), "secret_fingerprint": "randomblob(32)"}},
		{"donations", fmt.Sprintf("id=%d", id), map[string]string{"id": shift(id)}},
		{"donation_keys", fmt.Sprintf("id=%d", keyID), map[string]string{"id": shift(keyID), "donation_id": shift(id), "endpoint_key_id": shift(key), "source_endpoint_key_id": shift(key), "report_fingerprint": "randomblob(32)", "canonical_base_url": url}},
		{"donation_handling", fmt.Sprintf("donation_id=%d", id), map[string]string{"donation_id": shift(id)}},
		{"donation_key_memberships", fmt.Sprintf("donation_id=%d", id), map[string]string{"endpoint_key_id": shift(key), "donation_key_id": shift(keyID), "donation_id": shift(id)}},
	} {
		rows, err := tx.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table.name)
		if err != nil {
			t.Fatal(err)
		}
		var columns, values []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			quoted := `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
			columns = append(columns, quoted)
			value, ok := table.replace[column]
			if !ok {
				value = "original." + quoted
			}
			values = append(values, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		query := `WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT INTO "` + table.name + `" (` + strings.Join(columns, ",") + `) SELECT ` + strings.Join(values, ",") + ` FROM (SELECT * FROM "` + table.name + `" WHERE ` + table.where + `) original CROSS JOIN seq`
		if _, err := tx.Exec(query, count-1); err != nil {
			t.Fatalf("seed %s: %v", table.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := e.store.DB().QueryRow(`SELECT nbi_donation_source(mainstream_channel_id,connector_type,canonical_base_url) FROM donation_keys WHERE id=?`, keyID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSourcePagesUseCompleteIndexedCollectionsAtScale(t *testing.T) {
	for _, distinct := range []bool{true, false} {
		t.Run(fmt.Sprintf("distinct=%t", distinct), func(t *testing.T) {
			dbtest.Scale(t, func(t *testing.T) {
				e := newDonationTestEnv(t)
				owner := e.seedUser(t, "scale-donor", nil, false)
				e.seedUser(t, "", nil, true)
				const count = 10017
				source := seedBrowseScale(t, e, owner, count, distinct)
				ctx := context.Background()
				for _, size := range []int{10, 20, 50, 100} {
					out, err := e.service.SourcesAdminPage(ctx, SourceFilter{}, pagination.Request{Page: pagination.MaxPage, Size: size})
					if err != nil {
						t.Fatal(err)
					}
					if distinct {
						if out.Pagination.TotalItems != "10017" || len(out.Data) != (count-1)%size+1 {
							t.Fatal(out.Pagination, len(out.Data))
						}
					} else if out.Pagination.TotalItems != "1" || len(out.Data) != 1 || out.Data[0].KeyCount != "10017" || out.Data[0].DonationCount != "10017" || out.Data[0].PendingDonationCount != "10017" {
						t.Fatal(out)
					}
					for _, row := range out.Data {
						if row.UsableKeyCount != "0" {
							t.Fatal(row)
						}
					}
					keys, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{}, pagination.Request{Page: pagination.MaxPage, Size: size})
					if err != nil {
						t.Fatal(err)
					}
					if distinct {
						if keys.Pagination.TotalItems != "1" || len(keys.Data) != 1 {
							t.Fatal(keys.Pagination, len(keys.Data))
						}
					} else if keys.Pagination.TotalItems != "10017" || len(keys.Data) != (count-1)%size+1 {
						t.Fatal(keys.Pagination, len(keys.Data))
					}
				}
				query, args := sourceSelectionSQL(SourceFilter{}, nil, e.clock.Load())
				rows, err := e.store.DB().Query(`EXPLAIN QUERY PLAN SELECT `+sourceGroupColumns+`,COUNT(*) FROM (`+query+`) GROUP BY `+sourceGroupColumns+` ORDER BY `+sourceGroupColumns+` LIMIT 100 OFFSET 10000`, args...)
				if err != nil {
					t.Fatal(err)
				}
				var plan strings.Builder
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					plan.WriteString(detail)
					plan.WriteByte('\n')
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(plan.String(), "idx_donation_keys_source_page") || strings.Contains(plan.String(), "TEMP B-TREE FOR GROUP BY") || strings.Contains(plan.String(), "TEMP B-TREE FOR ORDER BY") {
					t.Fatal(plan.String())
				}
				t.Log(plan.String())
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := e.service.SourcesAdminPage(cancelled, SourceFilter{}, pagination.Default()); err == nil {
					t.Fatal("cancelled query succeeded")
				}
				var violations int
				if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
					t.Fatal(violations, err)
				}
			})
		})
	}
}
