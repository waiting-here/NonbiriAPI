package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

var retainedSourceManifests = []struct{ name, hash string }{
	{"before_routing", preRoutingManifestHash},
	{"before_key_limits", preKeyLimitsManifestHash},
	{"before_response_starts", preResponseStartsManifestHash},
	{"complete", preBetaTwoManifestHash},
	{"recurring_limits", preBrowseManifestHash},
	{"browse_indexes", preQuotaCleanupManifestHash},
	{"quota_cleanup_indexes", preStewardHoldReadManifestHash},
}

// The fixture uses only synthetic identities and a credential sealed by the
// real codec. Existing rows are compared by all columns, including SQL types.
func seedRetainedBusinessData(t *testing.T, store *Store, vault *secret.Vault) {
	t.Helper()
	database := store.DB()
	database.SetMaxOpenConns(1)
	zero, one := hostileBlob16(0), hostileBlob16(1)
	users := []int64{hostileInsertUser(t, database, "retained-a", 0, 100),
		hostileInsertUser(t, database, "retained-b", 0, 100),
		hostileInsertUser(t, database, "retained-c", 1, 100)}
	wallet := hostileInsertAccount(t, database, "user", users[0], nil, 1, hostileBlob16(37), 100)
	hostileMustExec(t, database, `UPDATE credit_accounts SET balance_sign=-1,balance_mag=? WHERE code='external'`, hostileBlob16(37))
	opID := hostileOID("op_")
	hostileInsertOperation(t, database, opID, 1, "admin_user_adjustment", "operation", opID)
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,0,?,'user',1,?)`, opID, wallet, hostileBlob16(37))
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
SELECT ?,1,id,'external',-1,?,-1,? FROM credit_accounts WHERE code='external'`, opID, hostileBlob16(37), hostileBlob16(37))
	hostileMustExec(t, database, `UPDATE users SET total_requests=?,total_output_tokens=?,revision=? WHERE id=?`, hostileBlob16(17), hostileBlob16(29), one, users[0])
	hostileMustExec(t, database, `INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at)
VALUES(?,7,?,'test-head','test-tail',101,103)`, users[0], hostileBlob32(9))
	hostileMustExec(t, database, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES('synthetic-session-hash',?,103,1800000023,1800000023,101,'7')`, users[0])
	hostileMustExec(t, database, `UPDATE site_config SET value='Retained site',updated_at=103 WHERE key='site_name'`)
	hostileMustExec(t, database, `UPDATE site_config SET value='Custom privacy text with historical promises.',updated_at=104 WHERE key='legal_privacy_override_en'`)
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(hostileBlob16(47))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("retained-test-credential"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	secretID := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,'https://upstream.example/v1','openai-compatible',?,101)`, credentialContext.ContextID(), envelope))
	endpoint := hostileInsertEndpoint(t, database, users[0], "https://upstream.example/v1")
	keyID := hostileInsertEndpointKey(t, database, endpoint, secretID)
	hostileMustExec(t, database, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,3,17)`, keyID)
	donation := hostileInsertDonation(t, database, users[0])
	donationKey := hostileInsertDonationKey(t, database, donation, keyID)
	hostileMustExec(t, database, `UPDATE donations SET status='approved',description='Retained donation',created_at=101,updated_at=109 WHERE id=?`, donation)
	hostileMustExec(t, database, `UPDATE donation_keys SET calls_used=?,tokens_used=?,expires_at=1800000023 WHERE id=?`, hostileBlob16(3), hostileBlob16(41), donationKey)
	hostileMustExec(t, database, `INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,101)`, keyID, donationKey, donation)
	hostileMustExec(t, database, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,103)`, keyID)
	hostileMustExec(t, database, `INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at)
VALUES(?,'manual','upstream','upstream','provider',1,101,103)`, keyID)
	model := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,route_strategy,created_at,updated_at)
VALUES(?,'provider','model','provider/model','random',101,103)`, users[0]))
	hostileMustExec(t, database, `INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,'upstream',0,101,103)`, model, keyID)
	charityModel := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,discount_enabled,discount_percent,discount_start_at,discount_end_at,created_by_user_id,created_at,updated_at)
VALUES('provider','model','[公益]provider/model',1,'per_request',120,13,1,50,101,1800000023,?,101,103)`, users[2]))
	hostileMustExec(t, database, `INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream',0,101,103)`, charityModel, donationKey, keyID)
	hostileMustExec(t, database, `INSERT INTO charity_model_routing(model_id,strategy) VALUES(?,'expiry_weighted')`, charityModel)
	reqID, claimID := hostileOID("req_"), hostileOID("clm_")
	hostileInsertTerminalRequest(t, database, reqID, users[0], "openai_chat_completions", "success", 200, nil)
	hostileMustExec(t, database, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,claim_now,state,dispatched_at,terminal_at)
VALUES(?,?,1,'self',?,0,'committed',0,1)`, claimID, reqID, keyID)
	hostileMustExec(t, database, `INSERT INTO dispatch_response_starts(claim_id,started_at) VALUES(?,0)`, claimID)
	logID := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,upstream_model_id,endpoint_base_url,caller_result_class,caller_status,status_code,attempt_count,started_at,completed_at)
VALUES(?,?,'openai_chat_completions','model','upstream','https://upstream.example/v1','success',200,200,1,0,1)`, reqID, users[0]))
	hostileInsertAttempt(t, database, claimID, logID, 1)
	hostileMustExec(t, database, `INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at)
VALUES('control_mutation',?,?,?,'completed',201,?,101,86501)`, hostileBlob32(1), hostileBlob32(2), hostileBlob32(3), []byte(`{"retained":true}`))
	hostileMustExec(t, database, `INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,created_at,terminal_at)
VALUES(?,'model_discovery',?,'user',?,'completed','preserved',101,103)`, hostileOIDVariant("op_", 'D', 'Q'), users[0], hostileBlob32(4))
	hostileMustExec(t, database, `INSERT INTO announcements(id,state,revision,draft_title_zh,draft_body_zh,draft_title_en,draft_body_en,published_title_zh,published_body_zh,published_title_en,published_body_en,severity,published_revision,published_at,created_at,updated_at)
VALUES(?,'published',2,'公告','**正文**','Announcement','**Body**','公告','**正文**','Announcement','**Body**','info',2,103,101,103)`, hostileOID("ann_"))
	hostileInsertAnnouncementAudit(t, database, users[2], 101)
	// An existing activity record and its ledger source must retain their time.
	welfareOp := hostileOIDVariant("op_", 'W', 'Q')
	hostileInsertOperation(t, database, welfareOp, 2, "welfare_claim", "operation", welfareOp)
	hostileMustExec(t, database, `INSERT INTO welfare_claims(user_id,site_day,operation_id,threshold_milli,cap_milli,pool_before_milli,award_milli,created_at)
VALUES(?,'1970-01-01',?,0,1,1,1,105)`, users[1], welfareOp)
	var currentPool string
	if err := database.QueryRow(`SELECT id FROM shared_pools WHERE pool_type='thursday' AND period_id IS NULL`).Scan(&currentPool); err != nil {
		t.Fatal(err)
	}
	periodID, nextPool := hostileOID("thu_"), hostileOIDVariant("pol_", 'N', 'Q')
	hostileMustExec(t, database, `PRAGMA defer_foreign_keys=ON`)
	hostileMustExec(t, database, `BEGIN`)
	hostileMustExec(t, database, `UPDATE shared_pools SET period_id=? WHERE id=?`, periodID, currentPool)
	nextAccount := hostileInsertAccount(t, database, "pool", nil, "pool:"+nextPool, 0, zero, 100)
	hostileInsertSharedPool(t, database, nextPool, "thursday", "", nextAccount)
	hostileInsertThursdayPeriod(t, database, periodID, currentPool, nextPool, "open")
	hostileMustExec(t, database, `INSERT INTO thursday_participants(period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,payout_mag,ledger_rows_remaining,created_at,updated_at)
VALUES(?,?,?, ?,?,0,?,?,105,105)`, periodID, hostileOID("thp_"), users[0], one, one, zero, one)
	hostileMustExec(t, database, `COMMIT`)
	active := hostileOIDVariant("rps_", 'A', 'Q')
	hostileInsertRPSSession(t, database, active, "gesture", "started", hostileInsertRPSAccount(t, database, active))
	for seat, user := range users {
		hostileInsertRPSSeat(t, database, active, seat, user, nil, nil, nil, nil, nil, "active")
	}
	finished := hostileOIDVariant("rps_", 'F', 'Q')
	hostileMustExec(t, database, `INSERT INTO game_rps_summaries(session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,started_at,terminal_at,terminal_reason,base_round_count,paid_tie_count,free_tie_count,total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,platform_total,welfare_total,thursday_total,delete_at)
VALUES(?,'quick',1,5,0,0,0,100,200,'quick_resolved',?,?,?,?,?,?,?,?,?,?,2592200)`, finished, one, zero, zero, zero, one, one, one, zero, zero, zero)
	for seat, user := range users {
		hostileMustExec(t, database, `INSERT INTO game_rps_summary_seats(session_id,seat_no,user_id,input,returned,wallet_net_sign,wallet_net_mag,timeout_count,rock_count,scissors_count,paper_count)
VALUES(?,?,?,?,?,0,?,?,?,?,?)`, finished, seat, user, hostileBlob32(5), hostileBlob32(5), zero, zero, one, one, one)
		// The first player has already acknowledged; the other two have not.
		if seat > 0 {
			hostileMustExec(t, database, `INSERT INTO game_rps_pending_results(user_id,session_id_text,mode,terminal_reason,own_seat_no,own_input,own_returned,own_wallet_net_sign,own_wallet_net_mag,seat0_result,seat1_result,seat2_result,created_at)
VALUES(?,?,'quick','quick_resolved',?,?,?,0,?,'tie','tie','tie',200)`, user, finished, seat, hostileBlob32(5), hostileBlob32(5), zero)
		}
	}
	batch := hostileOID("fb_")
	hostileInsertFishingBatch(t, database, batch, users[0], hostileOIDVariant("op_", 'F', 'Q'))
	hostileMustExec(t, database, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'boot','junk',0,0)`, batch)
	hostileMustExec(t, database, `UPDATE game_fishing_batches SET state='committed',payout_total_milli=0,ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=105,revealed_at=106 WHERE id=?`, zero, batch)
	hostileMustExec(t, database, `INSERT INTO game_fishing_rank_facts(batch_id_text,user_id,settled_at,expires_at,payout_total,aggregate_applied) VALUES(?,?,105,2592105,?,1)`, batch, users[0], zero)
	hostileMustExec(t, database, `INSERT INTO game_fishing_rank_aggregates(user_id,batch_count,total_payout,score_achieved_at,public_tie_key,revision,updated_at) VALUES(?,?,?,105,?,?,105)`, users[0], one, zero, hostileBlob32(21), one)
	hostileMustExec(t, database, `INSERT INTO game_linklink_summaries(session_id,user_id,spec,price_milli,terminal_reason,started_at,deadline,terminal_at,pairs_removed,score)
VALUES(?,?,'10x10',0,'completed',101,1101,201,50,99)`, hostileOID("ll_"), users[0])
	hostileMustExec(t, database, `UPDATE credit_capacity SET last_ledger_seq=2,reserved_future_rows=?,revision=? WHERE id=1`, hostileBlob16(3), hostileBlob16(7))
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func makeRetainedSource(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	if want == preModelTokenReserveManifestHash {
		hostileMustExec(t, database, `DROP TABLE charity_model_token_reserves`)
		assertRetainedManifest(t, database, want)
		return
	}
	if want == preBrowseManifestHash || want == preQuotaCleanupManifestHash || want == preStewardHoldReadManifestHash {
		if want == preStewardHoldReadManifestHash {
			dropFishingLengthObjects(t, database)
			hostileMustExec(t, database, `DROP TABLE legal_hold_steward_reads`)
		} else if want == preBrowseManifestHash {
			dropBrowseIndexes(t, database)
		} else {
			dropQuotaCleanupIndexes(t, database)
		}
		tx, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateBetaTwoDefaults(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, want)
		return
	}
	dropBetaTwoAdditiveObjects(t, database)
	if want != preBetaTwoManifestHash {
		hostileMustExec(t, database, `DROP TABLE dispatch_response_starts`)
	}
	if want == preRoutingManifestHash || want == preKeyLimitsManifestHash {
		hostileMustExec(t, database, `DROP TABLE endpoint_key_limits; DROP INDEX idx_dispatch_claims_key_active; DROP INDEX idx_dispatch_claims_key_rpm`)
	}
	if want == preRoutingManifestHash {
		hostileMustExec(t, database, `DROP TABLE charity_model_routing`)
	}
	assertRetainedManifest(t, database, want)
}

func assertRetainedManifest(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil || generationManifestDigest(manifest) != want {
		t.Fatalf("manifest = %s, want %s: %v", generationManifestDigest(manifest), want, err)
	}
}

type retainedTableImage struct {
	Columns []string
	Rows    int
	Digest  [32]byte
}

func retainedTableImages(t *testing.T, database *sql.DB, tables []string) map[string]retainedTableImage {
	t.Helper()
	if tables == nil {
		rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	images := make(map[string]retainedTableImage, len(tables))
	for _, table := range tables {
		rows, err := database.Query(`SELECT * FROM ` + hostileQuoteIdent(table))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var encoded []string
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			typed := make([]any, len(values))
			for i, value := range values {
				typed[i] = []any{fmt.Sprintf("%T", value), value}
			}
			data, err := json.Marshal(typed)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, string(data))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(encoded)
		images[table] = retainedTableImage{columns, len(encoded), sha256.Sum256([]byte(strings.Join(encoded, "\n")))}
	}
	return images
}

func assertRetainedImages(t *testing.T, database *sql.DB, before map[string]retainedTableImage) {
	t.Helper()
	tables := make([]string, 0, len(before))
	for table := range before {
		tables = append(tables, table)
	}
	after := retainedTableImages(t, database, tables)
	for table, old := range before {
		if !reflect.DeepEqual(old, after[table]) {
			t.Errorf("retained table %s changed (rows %d -> %d)", table, old.Rows, after[table].Rows)
		}
	}
}

func TestRetainedBusinessDataAcrossEverySupportedSource(t *testing.T) {
	for _, source := range retainedSourceManifests {
		t.Run(source.name, func(t *testing.T) {
			path, vault := bootstrapTestPath(t, "retained.sqlite"), bootstrapTestVault(t)
			store, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedRetainedBusinessData(t, store, vault)
			makeRetainedSource(t, store.DB(), source.hash)
			before := retainedTableImages(t, store.DB(), nil)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			var upgraded map[string]retainedTableImage
			for attempt := 0; attempt < 2; attempt++ {
				store, err = Open(path, vault)
				if err != nil {
					t.Fatalf("open %d: %v", attempt, err)
				}
				defer store.Close()
				assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
				assertRetainedImages(t, store.DB(), before)
				if attempt == 0 {
					upgraded = retainedTableImages(t, store.DB(), nil)
				} else {
					assertRetainedImages(t, store.DB(), upgraded)
				}
				if countRows(t, store, `SELECT COUNT(*) FROM donation_handling WHERE state='legacy' AND created_at=101 AND updated_at=109`) != 1 ||
					countRows(t, store, `SELECT COUNT(*) FROM charity_model_access WHERE allowed_level_mask=31 AND public_description=''`) != 1 {
					t.Fatal("missing legacy defaults")
				}
				for _, table := range []string{"game_rps_presentation", "game_rps_pending_presentation", "game_rps_summary_presentation"} {
					if countRows(t, store, `SELECT COUNT(*) FROM `+hostileQuoteIdent(table)) != 0 {
						t.Fatalf("old game received guessed values in %s", table)
					}
				}
				if countRows(t, store, `SELECT COUNT(*) FROM game_fishing_length_facts WHERE species_key='boot' AND size_cm=0 AND blue_fat_fish_length_cm IS NULL`) != 1 ||
					countRows(t, store, `SELECT COUNT(*) FROM game_fishing_outcome_lengths`) != 0 ||
					countRows(t, store, `SELECT COUNT(*) FROM game_fishing_best_lengths`) != 0 {
					t.Fatal("retained fishing maximum missing or an Easter egg was invented")
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRetainedExtensionRollsBackWhenStorageFills(t *testing.T) {
	for _, source := range retainedSourceManifests {
		t.Run(source.name, func(t *testing.T) {
			store := openTestStore(t, bootstrapTestPath(t, "full.sqlite"))
			seedRetainedBusinessData(t, store, bootstrapTestVault(t))
			makeRetainedSource(t, store.DB(), source.hash)
			// Reclaim dropped pages before imposing a small additional page budget.
			hostileMustExec(t, store.DB(), `VACUUM`)
			before := retainedTableImages(t, store.DB(), nil)
			var pages int
			if err := store.DB().QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
				t.Fatal(err)
			}
			extraPages := 5
			if source.hash == preStewardHoldReadManifestHash {
				// This source needs a tight bound to fail inside the additions.
				extraPages = 1
			}
			hostileMustExec(t, store.DB(), fmt.Sprintf(`PRAGMA max_page_count=%d`, pages+extraPages))
			err := extendKnownGenerationTwoSchema(context.Background(), store.DB())
			if err == nil || !strings.Contains(err.Error(), "full") {
				t.Fatalf("expected storage-full failure, got %v", err)
			}
			assertRetainedManifest(t, store.DB(), source.hash)
			assertRetainedImages(t, store.DB(), before)
			hostileMustExec(t, store.DB(), `PRAGMA max_page_count=2147483647`)
			if err := extendKnownGenerationTwoSchema(context.Background(), store.DB()); err != nil {
				t.Fatalf("retry: %v", err)
			}
			assertRetainedImages(t, store.DB(), before)
		})
	}
}

func TestRetainedExtensionRejectsMixedSourcesWithoutWriting(t *testing.T) {
	for _, source := range retainedSourceManifests {
		t.Run(source.name, func(t *testing.T) {
			path, vault := bootstrapTestPath(t, "mixed.sqlite"), bootstrapTestVault(t)
			store, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			seedRetainedBusinessData(t, store, vault)
			makeRetainedSource(t, store.DB(), source.hash)
			hostileMustExec(t, store.DB(), `DROP INDEX idx_users_created`)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotBootstrapSources(t, path)
			other, err := Open(path, vault)
			if other != nil {
				other.Close()
				t.Fatal("modified source accepted")
			}
			if err == nil {
				t.Fatal("missing startup rejection")
			}
			if after := snapshotBootstrapSources(t, path); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected source was modified")
			}
		})
	}
}
