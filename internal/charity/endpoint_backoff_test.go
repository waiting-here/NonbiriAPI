package charity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

var creditRules = []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "auto"}}

func (e *backoffEnv) endpointPolicy(t *testing.T, rules []smartbackoff.Rule) {
	t.Helper()
	encoded, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.DB().Exec(`INSERT INTO endpoint_backoff_policies(connector_type,base_url,revision,rules_json) VALUES('openai-compatible','https://charity.example.test/v1',1,?) ON CONFLICT(connector_type,base_url) DO UPDATE SET revision=revision+1,rules_json=excluded.rules_json`, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
}

func creditOutcome(g *claim.Dispatch) claim.AttemptOutcome {
	c := upstreamerror.Context{CharityCall: true, Rules: g.BackoffRules(), ContainsSecret: func([]byte) bool { return false }}
	d := c.Parse([]byte(`{"error":{"code":"insufficient_credits","type":"billing_error","message":"Insufficient credits"}}`))
	return claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 402, FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakUpstreamFailure, Backoff: d.Backoff()}
}

func TestEndpointBackoffDispatchSnapshotAndPolicyEdits(t *testing.T) {
	e := newBackoffEnv(t)
	e.endpointPolicy(t, creditRules)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET failure_streak=? WHERE id=?`, u128Blob(t, 2), e.donationKey); err != nil {
		t.Fatal(err)
	}
	h, g := e.dispatch(t)
	e.endpointPolicy(t, []smartbackoff.Rule{})
	e.complete(t, h, creditOutcome(g))
	e.wantWaiting(t, 900)
	if e.status(t).Source != "custom_rule" {
		t.Fatal(e.status(t))
	}
	var revision, index int
	var rules string
	if err := e.store.DB().QueryRow(`SELECT policy_revision,rules_json,rule_index FROM dispatch_smart_backoff_facts WHERE claim_id=?`, h.ClaimID()).Scan(&revision, &rules, &index); err != nil || revision != 1 || index != 1 || rules == "[]" {
		t.Fatal(revision, rules, index, err)
	}
	var streak []byte
	if err := e.store.DB().QueryRow(`SELECT failure_streak FROM donation_keys WHERE id=?`, e.donationKey).Scan(&streak); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 2, "custom rule keeps previous streak")
	before := *e.status(t)
	e.endpointPolicy(t, []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "week"}})
	if *e.status(t) != before {
		t.Fatal("policy edit cleared current wait")
	}
	e.ready(t)
	trial, grant := e.dispatch(t)
	if !grant.IsBackoffTrial() || grant.BackoffRules()[0].Period != "week" {
		t.Fatal("trial did not use latest rules")
	}
	e.endpointPolicy(t, []smartbackoff.Rule{})
	e.complete(t, trial, creditOutcome(grant))
	e.wantWaiting(t, 21600)
	e.complete(t, trial, creditOutcome(grant))
	e.wantWaiting(t, 21600)
	e.ready(t)
	trial, grant = e.dispatch(t)
	if len(grant.BackoffRules()) != 0 {
		t.Fatal("cleared rules returned")
	}
	e.complete(t, trial, claim.AttemptOutcome{Kind: claim.ResultSynthetic, FailureOrigin: contract.OriginClientCancel, StreakDisposition: contract.StreakNeutral})
	e.wantWaiting(t, 21600)
	if e.status(t).Source != "custom_rule" {
		t.Fatal("clearing rules changed historical source")
	}
}

func TestEndpointBackoffClaimProofAndHeaderPriority(t *testing.T) {
	for _, scenario := range []string{"unconfigured", "wrong index", "wrong period", "zero header", "past header", "accepted"} {
		t.Run(scenario, func(t *testing.T) {
			e := newBackoffEnv(t)
			if scenario != "unconfigured" {
				e.endpointPolicy(t, []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "day"}})
			}
			h, g := e.dispatch(t)
			outcome := creditOutcome(g)
			switch scenario {
			case "unconfigured":
				outcome.Backoff = smartbackoff.Classification{Source: "custom_rule", Period: "unknown", RuleIndex: 1}
			case "wrong index":
				outcome.Backoff.RuleIndex = 2
			case "wrong period":
				outcome.Backoff.Period = "month"
			case "zero header", "past header":
				until := e.clock.Load()
				if scenario == "past header" {
					until--
				}
				if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 402, ObservedAt: e.clock.Load(), RateLimitedUntil: until}); err != nil {
					t.Fatal(err)
				}
			case "accepted":
				e.complete(t, h, outcome)
				e.ready(t)
				h, g = e.dispatch(t)
				outcome = creditOutcome(g)
				if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{Status: 200, ObservedAt: e.clock.Load()}); err != nil {
					t.Fatal(err)
				}
			}
			e.complete(t, h, outcome)
			if e.status(t) != nil {
				t.Fatal("unproven or superseded classification changed state", e.status(t))
			}
			var streak []byte
			if err := e.store.DB().QueryRow(`SELECT failure_streak FROM donation_keys WHERE id=?`, e.donationKey).Scan(&streak); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if scenario == "unconfigured" || scenario == "wrong index" || scenario == "wrong period" {
				want = 1
			}
			assertU128(t, streak, want, "classification proof")
		})
	}
}

func (e *backoffEnv) additionalEndpointKey(t *testing.T, marker byte, kind, base string) (int64, int64, int64) {
	t.Helper()
	contextID := bytes.Repeat([]byte{marker}, 16)
	vault, err := secret.New(bytes.Repeat([]byte{charityTestMasterKeyByte}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ctx, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte(fmt.Sprint("test-key-", marker)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) int64 {
		t.Helper()
		r, err := e.store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := r.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	endpoint := exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,?,?,'',1,1,?,?)`, e.donorID, kind, base, e.clock.Load(), e.clock.Load())
	secretID := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,?,?,?)`, contextID, base, kind, envelope, e.clock.Load())
	key := exec(`INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at) VALUES(?,?,?,'head','tail','',1,0,1,?,?)`, endpoint, secretID, bytes.Repeat([]byte{marker}, 32), e.clock.Load(), e.clock.Load())
	z, one := u128Blob(t, 0), u128Blob(t, 1)
	donation := exec(`INSERT INTO donation_keys(donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,token_reserve,enabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,source_endpoint_key_id,report_fingerprint) VALUES(?,?,'head','tail',?,?,?,?,?,?,?,?,5,1,?,?,?,?,'',?,?,?,?)`, e.donationID, key, base, kind, z, z, z, z, z, z, z, one, one, one, e.clock.Load(), e.clock.Load(), key, bytes.Repeat([]byte{marker}, 32))
	exec(`INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,?)`, key, donation, e.donationID, e.clock.Load())
	exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'upstream-model',1,0,1,1,?)`, key, e.clock.Load())
	exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream-model',?,?,?)`, e.tokenModel, donation, key, int(marker), e.clock.Load(), e.clock.Load())
	return endpoint, key, donation
}

func TestEndpointBackoffSharedByNewKeysWithExactEndpointIsolation(t *testing.T) {
	e := newBackoffEnv(t)
	e.endpointPolicy(t, creditRules)
	h, g := e.dispatch(t)
	e.complete(t, h, creditOutcome(g))
	e.wantWaiting(t, 900)
	for i, tc := range []struct {
		kind, base string
		want       bool
	}{
		{"openai-compatible", "https://charity.example.test/v1", true},
		{"openai-compatible", "https://charity.example.test/other", false},
		{"anthropic-compatible", "https://charity.example.test/v1", false},
	} {
		endpoint, key, donation := e.additionalEndpointKey(t, byte(i+7), tc.kind, tc.base)
		r := e.request(t, e.tokenModel)
		input := e.retryClaimInput(r.ID)
		input.DonationKeyID = donation
		input.Candidate.EndpointID = endpoint
		input.Candidate.EndpointKeyID = key
		input.Candidate.ConnectorType = contract.Type(tc.kind)
		input.Candidate.CanonicalBaseURL = tc.base
		h, err := e.rail.Claim(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		g, err := e.rail.TakeForDispatch(t.Context(), h)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Clear()
		outcome := creditOutcome(g)
		if outcome.Backoff.Valid() != tc.want {
			t.Fatal(tc, outcome.Backoff)
		}
		e.complete(t, h, outcome)
		projection, err := smartbackoff.Read(t.Context(), e.store.DB(), donation, e.clock.Load())
		if err != nil || (projection.Backoff != nil) != tc.want {
			t.Fatal(tc, projection, err)
		}
	}
	// Owner calls never receive the endpoint rules or create dispatch backoff facts.
	r, err := e.rail.Accept(t.Context(), claim.AcceptInput{UserID: e.donorID, Route: claim.RouteOpenAIChat, ModelSnapshot: "private", AttemptLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := e.retryClaimInput(r.ID)
	input.ActorUserID = e.donorID
	input.Purpose = claim.PurposeSelf
	input.DonationKeyID = 0
	h, err = e.rail.Claim(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	g, err = e.rail.TakeForDispatch(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Clear()
	if len(g.BackoffRules()) != 0 {
		t.Fatal("owner received charity rules")
	}
}

func TestEndpointBackoffUpgradePreservesWaitingAndInFlight(t *testing.T) {
	for _, state := range []string{"waiting", "trial", "accepted"} {
		t.Run(state, func(t *testing.T) {
			e := newBackoffEnv(t)
			h, _ := e.dispatch(t)
			e.complete(t, h, quotaOutcome("day"))
			if state != "waiting" {
				e.ready(t)
				h, _ = e.dispatch(t)
				if state == "accepted" {
					if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{Status: 200, ObservedAt: e.clock.Load()}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := e.store.DB().Exec(`INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at) VALUES(?,?,?)`, e.donationKey, e.clock.Load()+123, e.clock.Load()); err != nil {
				t.Fatal(err)
			}
			e.upgradeBackoffSchema(t, 26, "")
			if _, err := e.rail.RecoverNonterminal(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			if state == "accepted" {
				if e.status(t) != nil {
					t.Fatal("accepted trial not restored")
				}
			} else {
				e.wantWaiting(t, 900)
			}
			var until int64
			if err := e.store.DB().QueryRow(`SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?`, e.donationKey).Scan(&until); err != nil || until != e.clock.Load()+123 {
				t.Fatal("header changed", until, err)
			}
			var count int
			if err := e.store.DB().QueryRow(`SELECT count(*) FROM endpoint_backoff_policies`).Scan(&count); err != nil || count != 0 {
				t.Fatal("migration invented policies", count, err)
			}
		})
	}
}

func TestEndpointBackoffExportAndDeletionKeepPlatformPolicySeparate(t *testing.T) {
	e := newBackoffEnv(t)
	if _, err := e.store.DB().Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'legacy',1,?,?)`, e.donationID, e.clock.Load(), e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	e.endpointPolicy(t, creditRules)
	h, g := e.dispatch(t)
	e.complete(t, h, creditOutcome(g))
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := e.donation.ExportUserTx(t.Context(), tx, e.donorID, e.clock.Load(), 100)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if len(data) != 1 || data[0].Keys[0].Backoff == nil || data[0].Keys[0].Backoff.Source != "custom_rule" {
		tx.Rollback()
		t.Fatal("state missing from export")
	}
	for _, field := range []string{"insufficient_credits", "rules_json", "rule_index", "policy_revision"} {
		if strings.Contains(string(raw), field) {
			tx.Rollback()
			t.Fatal("platform policy exported", field)
		}
	}
	if err := e.donation.PrepareAccountDeletion(t.Context(), tx, e.donorID, e.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if e.status(t) != nil {
		t.Fatal("deleted owner retained key state")
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT count(*) FROM endpoint_backoff_policies`).Scan(&count); err != nil || count != 1 {
		t.Fatal("owner deletion removed platform policy", count, err)
	}
	// An ended resource cannot regain a runtime episode from a late result.
	e.complete(t, h, creditOutcome(g))
	if e.status(t) != nil {
		t.Fatal("late completion revived deleted owner state")
	}
}
