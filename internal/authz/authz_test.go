package authz

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const authTestNow = int64(1_800_000_000)

func openAuthStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authz.db")
	master := bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	store.DB().SetMaxOpenConns(8)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertAuthUser(t *testing.T, database *sql.DB, discord, token, generation string, admin bool, manualLevel *int64) int64 {
	t.Helper()
	zero := make([]byte, 16)
	revision := make([]byte, 16)
	revision[15] = 1
	adminValue := 0
	if admin {
		adminValue = 1
	}
	result, err := database.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,is_banned,donation_credit_mag,
 level,auto_level,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,? ,?,0,?, ?,4,?,?,?,?,?,?,?, ?,?)`,
		discord, "user-"+discord, adminValue, zero, manualLevel,
		zero, zero, zero, zero, zero, zero, revision, authTestNow-100, authTestNow-100,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, token, userID, authTestNow-10, authTestNow+600, authTestNow+1200, authTestNow-100, generation); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return userID
}

func beginAuthTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	return tx
}

func testAuthorizer(elevationConsumer ElevationConsumer) *Authorizer {
	return New(Options{
		Now:       func() time.Time { return time.Unix(authTestNow, 0) },
		Elevation: elevationConsumer,
	})
}

func TestAuthorizeLiveRolesAndOwnerIsolation(t *testing.T) {
	store := openAuthStore(t)
	userID := insertAuthUser(t, store.DB(), "1001", "session-user", "g1", false, nil)
	levelFive := int64(5)
	stewardID := insertAuthUser(t, store.DB(), "1002", "session-steward", "g2", false, &levelFive)
	adminID := insertAuthUser(t, store.DB(), "1003", "session-admin", "g3", true, nil)
	authorizer := testAuthorizer(nil)

	cases := []struct {
		name  string
		actor Actor
		role  Role
		want  error
	}{
		{"user", Actor{ActorUserSession, userID, "session-user", "g1", ""}, RoleUser, nil},
		{"user is not steward", Actor{ActorUserSession, userID, "session-user", "g1", ""}, RoleSteward, ErrForbidden},
		{"steward", Actor{ActorUserSession, stewardID, "session-steward", "g2", ""}, RoleSteward, nil},
		{"admin", Actor{ActorAdminSession, adminID, "session-admin", "g3", ""}, RoleAdministrator, nil},
		{"admin cookie cannot be user", Actor{ActorAdminSession, adminID, "session-admin", "g3", ""}, RoleUser, ErrForbidden},
		{"user cookie cannot be admin", Actor{ActorUserSession, userID, "session-user", "g1", ""}, RoleAdministrator, ErrForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tx := beginAuthTx(t, store.DB())
			defer tx.Rollback()
			principal, err := authorizer.Authorize(context.Background(), tx, test.actor, Requirement{Role: test.role})
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Authorize error=%v want %v", err, test.want)
				}
				return
			}
			if err != nil || principal.UserID != test.actor.UserID || principal.Role != test.role {
				t.Fatalf("Authorize=(%+v,%v)", principal, err)
			}
		})
	}

	result, err := store.DB().Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(?,'openai-compatible','https://owned.example/v1',?,?)`, userID, authTestNow, authTestNow)
	if err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}
	endpointID, _ := result.LastInsertId()
	owner := func(resourceID int64) OwnerPredicate {
		return OwnerPredicateFunc(func(ctx context.Context, tx *sql.Tx, actorID int64) (OwnershipResult, error) {
			var ownerID int64
			err := tx.QueryRowContext(ctx, `SELECT user_id FROM endpoints WHERE id=?`, resourceID).Scan(&ownerID)
			if errors.Is(err, sql.ErrNoRows) {
				return OwnershipMissing, nil
			}
			if err != nil {
				return 0, err
			}
			if ownerID != actorID {
				return OwnershipForeign, nil
			}
			return OwnershipOwned, nil
		})
	}
	actor := Actor{ActorUserSession, userID, "session-user", "g1", ""}
	for _, test := range []struct {
		name string
		id   int64
		want error
	}{{"owned", endpointID, nil}, {"foreign", endpointID, ErrNotFound}, {"missing", endpointID + 999, ErrNotFound}} {
		t.Run("owner "+test.name, func(t *testing.T) {
			candidate := actor
			if test.name == "foreign" {
				candidate = Actor{ActorUserSession, stewardID, "session-steward", "g2", ""}
			}
			tx := beginAuthTx(t, store.DB())
			defer tx.Rollback()
			_, err := authorizer.Authorize(context.Background(), tx, candidate, Requirement{Role: RoleUser, Owner: owner(test.id)})
			if test.want == nil && err != nil {
				t.Fatalf("owned authorization: %v", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("owner error=%v want %v", err, test.want)
			}
		})
	}
}

func TestAuthorizeRejectsBanSessionReplacementAndExpiry(t *testing.T) {
	store := openAuthStore(t)
	userID := insertAuthUser(t, store.DB(), "2001", "session-live", "g1", false, nil)
	authorizer := testAuthorizer(nil)
	actor := Actor{ActorUserSession, userID, "session-live", "g1", ""}

	assert := func(want error) {
		t.Helper()
		tx := beginAuthTx(t, store.DB())
		defer tx.Rollback()
		_, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleUser})
		if !errors.Is(err, want) {
			t.Fatalf("Authorize error=%v want %v", err, want)
		}
	}
	if _, err := store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	assert(ErrForbidden)
	if _, err := store.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, authTestNow, userID); err != nil {
		t.Fatal(err)
	}
	tx := beginAuthTx(t, store.DB())
	if _, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleUser}); err != nil {
		t.Fatalf("expired temporary ban should admit: %v", err)
	}
	_ = tx.Rollback()

	if _, err := store.DB().Exec(`UPDATE sessions SET cred_gen='g2' WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	assert(ErrUnauthorized)
	actor.SessionGeneration = "g2"
	if _, err := store.DB().Exec(`UPDATE sessions SET expires_at=?,absolute_expires_at=? WHERE token_hash=?`, authTestNow, authTestNow, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	assert(ErrUnauthorized)
	if _, err := store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	assert(ErrUnauthorized)
}

func TestAuthorizeConsumesFreshElevationAtFinalCheck(t *testing.T) {
	store := openAuthStore(t)
	adminID := insertAuthUser(t, store.DB(), "3001", "session-admin", "g1", true, nil)
	clock := time.Unix(authTestNow, 0)
	manager, err := elevation.NewManagerWithKey(bytes.Repeat([]byte{0x73}, 32), time.Minute)
	if err != nil {
		t.Fatalf("new elevation manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.SetClock(func() time.Time { return clock }); err != nil {
		t.Fatal(err)
	}
	authorizer := New(Options{Now: func() time.Time { return clock }, Elevation: manager})
	token, _, err := manager.IssueBound(adminID, elevation.KindAdmin, "session-admin")
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{ActorAdminSession, adminID, "session-admin", "g1", token}
	tx := beginAuthTx(t, store.DB())
	if _, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleAdministrator, FreshElevation: true}); err != nil {
		t.Fatalf("fresh elevation: %v", err)
	}
	_ = tx.Rollback()
	tx = beginAuthTx(t, store.DB())
	if _, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleAdministrator, FreshElevation: true}); !errors.Is(err, ErrElevatedRequired) {
		t.Fatalf("replayed elevation error=%v", err)
	}
	_ = tx.Rollback()

	token, _, err = manager.IssueBound(adminID, elevation.KindAdmin, "session-admin")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	actor.ElevationToken = token
	tx = beginAuthTx(t, store.DB())
	if _, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleAdministrator, FreshElevation: true}); !errors.Is(err, ErrElevatedRequired) {
		t.Fatalf("expired elevation error=%v", err)
	}
	_ = tx.Rollback()
}

func TestFinalTransactionDemotionShuffle(t *testing.T) {
	for _, demotionFirst := range []bool{false, true} {
		name := "authorization commits first"
		if demotionFirst {
			name = "demotion commits first"
		}
		t.Run(name, func(t *testing.T) {
			store := openAuthStore(t)
			levelFive := int64(5)
			userID := insertAuthUser(t, store.DB(), "4001", "session-steward", "g1", false, &levelFive)
			authorizer := testAuthorizer(nil)
			actor := Actor{ActorUserSession, userID, "session-steward", "g1", ""}
			if demotionFirst {
				if _, err := store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, userID); err != nil {
					t.Fatal(err)
				}
			}
			tx := beginAuthTx(t, store.DB())
			_, err := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleSteward})
			if demotionFirst {
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("demotion-first error=%v", err)
				}
				_ = tx.Rollback()
			} else {
				if err != nil {
					t.Fatalf("authorize first: %v", err)
				}
				if _, err := tx.Exec(`UPDATE users SET username='authorized-write' WHERE id=?`, userID); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if _, err := store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, userID); err != nil {
					t.Fatal(err)
				}
			}
			var username string
			if err := store.DB().QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&username); err != nil {
				t.Fatal(err)
			}
			if demotionFirst && username == "authorized-write" {
				t.Fatal("demotion-first request wrote domain state")
			}
			if !demotionFirst && username != "authorized-write" {
				t.Fatalf("authorized write=%q", username)
			}
		})
	}
}

func TestFinalTransactionRevocationShuffle(t *testing.T) {
	tests := []struct {
		name        string
		requirement func(*testing.T, *db.Store, int64) Requirement
		revoke      func(*testing.T, *db.Store, int64)
		want        error
	}{
		{
			name: "ban",
			requirement: func(*testing.T, *db.Store, int64) Requirement {
				return Requirement{Role: RoleUser}
			},
			revoke: func(t *testing.T, store *db.Store, userID int64) {
				t.Helper()
				if _, err := store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrForbidden,
		},
		{
			name: "session replacement",
			requirement: func(*testing.T, *db.Store, int64) Requirement {
				return Requirement{Role: RoleUser}
			},
			revoke: func(t *testing.T, store *db.Store, userID int64) {
				t.Helper()
				if _, err := store.DB().Exec(`UPDATE sessions SET cred_gen='replacement' WHERE user_id=?`, userID); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrUnauthorized,
		},
		{
			name: "account deletion",
			requirement: func(*testing.T, *db.Store, int64) Requirement {
				return Requirement{Role: RoleUser}
			},
			revoke: func(t *testing.T, store *db.Store, userID int64) {
				t.Helper()
				if _, err := store.DB().Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrUnauthorized,
		},
		{
			name: "owner change",
			requirement: func(t *testing.T, store *db.Store, userID int64) Requirement {
				t.Helper()
				_ = insertAuthUser(t, store.DB(), "shuffle-other", "shuffle-other-session", "other-generation", false, nil)
				if _, err := store.DB().Exec(`CREATE TABLE shuffle_owned_resources(id INTEGER PRIMARY KEY,owner_id INTEGER NOT NULL)`); err != nil {
					t.Fatal(err)
				}
				if _, err := store.DB().Exec(`INSERT INTO shuffle_owned_resources(id,owner_id) VALUES(1,?)`, userID); err != nil {
					t.Fatal(err)
				}
				return Requirement{Role: RoleUser, Owner: OwnerPredicateFunc(func(ctx context.Context, tx *sql.Tx, actorID int64) (OwnershipResult, error) {
					var ownerID int64
					if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM shuffle_owned_resources WHERE id=1`).Scan(&ownerID); err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							return OwnershipMissing, nil
						}
						return 0, err
					}
					if ownerID != actorID {
						return OwnershipForeign, nil
					}
					return OwnershipOwned, nil
				})}
			},
			revoke: func(t *testing.T, store *db.Store, userID int64) {
				t.Helper()
				var otherID int64
				if err := store.DB().QueryRow(`SELECT id FROM users WHERE discord_id='shuffle-other'`).Scan(&otherID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.DB().Exec(`UPDATE shuffle_owned_resources SET owner_id=? WHERE owner_id=?`, otherID, userID); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrNotFound,
		},
	}

	for _, test := range tests {
		for _, revocationFirst := range []bool{true, false} {
			order := "final transaction first"
			if revocationFirst {
				order = "revocation first"
			}
			t.Run(test.name+"/"+order, func(t *testing.T) {
				store := openAuthStore(t)
				if _, err := store.DB().Exec(`CREATE TABLE authz_effects(kind TEXT PRIMARY KEY)`); err != nil {
					t.Fatal(err)
				}
				userID := insertAuthUser(t, store.DB(), "shuffle-user", "shuffle-session", "shuffle-generation", false, nil)
				actor := Actor{
					Kind: ActorUserSession, UserID: userID, SessionTokenHash: "shuffle-session", SessionGeneration: "shuffle-generation",
				}
				requirement := test.requirement(t, store, userID)
				if revocationFirst {
					test.revoke(t, store, userID)
				}
				tx := beginAuthTx(t, store.DB())
				_, err := testAuthorizer(nil).Authorize(context.Background(), tx, actor, requirement)
				if revocationFirst {
					if !errors.Is(err, test.want) {
						t.Fatalf("Authorize error=%v want %v", err, test.want)
					}
					_ = tx.Rollback()
				} else {
					if err != nil {
						t.Fatalf("Authorize: %v", err)
					}
					if _, err := tx.Exec(`INSERT INTO authz_effects(kind) VALUES(?)`, test.name); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					test.revoke(t, store, userID)
				}
				var effects int
				if err := store.DB().QueryRow(`SELECT COUNT(*) FROM authz_effects`).Scan(&effects); err != nil {
					t.Fatal(err)
				}
				wantEffects := 1
				if revocationFirst {
					wantEffects = 0
				}
				if effects != wantEffects {
					t.Fatalf("effects=%d want %d", effects, wantEffects)
				}
			})
		}
	}
}

func TestFinalTransactionElevationExpiryShuffle(t *testing.T) {
	for _, expiryFirst := range []bool{true, false} {
		order := "final transaction first"
		if expiryFirst {
			order = "expiry first"
		}
		t.Run(order, func(t *testing.T) {
			store := openAuthStore(t)
			if _, err := store.DB().Exec(`CREATE TABLE authz_effects(kind TEXT PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			adminID := insertAuthUser(t, store.DB(), "elevation-shuffle", "elevation-session", "elevation-generation", true, nil)
			clock := time.Unix(authTestNow, 0)
			manager, err := elevation.NewManagerWithKey(bytes.Repeat([]byte{0x61}, 32), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			if err := manager.SetClock(func() time.Time { return clock }); err != nil {
				t.Fatal(err)
			}
			token, _, err := manager.IssueBound(adminID, elevation.KindAdmin, "elevation-session")
			if err != nil {
				t.Fatal(err)
			}
			authorizer := New(Options{Now: func() time.Time { return clock }, Elevation: manager})
			actor := Actor{
				Kind: ActorAdminSession, UserID: adminID, SessionTokenHash: "elevation-session",
				SessionGeneration: "elevation-generation", ElevationToken: token,
			}
			if expiryFirst {
				clock = clock.Add(2 * time.Minute)
			}
			tx := beginAuthTx(t, store.DB())
			_, err = authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleAdministrator, FreshElevation: true})
			if expiryFirst {
				if !errors.Is(err, ErrElevatedRequired) {
					t.Fatalf("expired elevation error=%v", err)
				}
				_ = tx.Rollback()
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(`INSERT INTO authz_effects(kind) VALUES('elevated')`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				clock = clock.Add(2 * time.Minute)
			}
			var effects int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM authz_effects`).Scan(&effects); err != nil {
				t.Fatal(err)
			}
			if effects != map[bool]int{true: 0, false: 1}[expiryFirst] {
				t.Fatalf("effects=%d", effects)
			}
		})
	}
}

func TestStableCode(t *testing.T) {
	for err, want := range map[error]string{
		ErrUnauthorized:     httperr.CodeUnauthorized,
		ErrForbidden:        httperr.CodeForbidden,
		ErrElevatedRequired: httperr.CodeElevationRequired,
		ErrNotFound:         httperr.CodeNotFound,
	} {
		if got := StableCode(err); got != want {
			t.Fatalf("StableCode(%v)=%q want %q", err, got, want)
		}
	}
	if got := StableCode(errors.New("db")); got != "" {
		t.Fatalf("internal stable code=%q", got)
	}
}
