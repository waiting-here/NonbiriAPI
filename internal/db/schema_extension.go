package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// This is the exact deployed schema immediately before per-model routing.
// Other incomplete or modified schemas remain invalid.
const preRoutingManifestHash = "22c5d92ad7b1b077298caa2220f41b911414248ed87acbdd7cf978eb29483c36"

const preKeyLimitsManifestHash = "8d39bd424d9df29960c6c113fd874833615de1d95e0f820042bb8b60d7678400"

const preResponseStartsManifestHash = "8fb054cef12ae7316f80d40994d9091a84b983a79868fdfef89e7f875c6a3ceb"

// preBetaTwoManifestHash is the complete beta.1 schema manifest digest.
// beta.2 extends from this baseline by applying the additive schema and
// seeding default sidecar rows in a single transaction.
const preBetaTwoManifestHash = "32d3e952512b7eb5c452e478eb9990b0518d51502d70bd93c195273980ba365d"

// The deployed recurring-limit schema already contains populated sidecars.
// Its extension adds only browse indexes and preserves every existing value.
const preBrowseManifestHash = "862d6c208018d2033c57bd8b87e3b324be5d83b2f2bd6729a7ce8cf9b4c96b9e"

// The deployed browse schema is extended by indexes only. Its populated
// recurring-limit facts and counters must remain unchanged.
const preQuotaCleanupManifestHash = "e9d0e725597515a9cfcb7a0463a636ecd179dca619cc9524a2db95d258a20aaa"

// The deployed schema before steward held-object read auditing.
const preStewardHoldReadManifestHash = "5a339c17dd63b975cd17f1bc946f0c799b46a68ce2d2576b8fa10b042315b3d1"

// The deployed fishing-length schema before per-model credit reservations.
const preModelTokenReserveManifestHash = "e6e10f9c37f0dff0ea9507173d48808aea6d448f05c9812cdb4ee5d3cc97ea36"

// The released schema before the one-hour recurring quota interval.
const preHourlyQuotaManifestHash = "8c0c7dc160170bae72e388c3f7b9c8cb15a756f867af92fa2644d2afd48a2550"

// The released schema before embedding request routes.
const preEmbeddingManifestHash = "956e85c750aec4ef451f5fda73a816af6474031b4795b76495d85d6715ddcc59"

func generationTwoExtensionNeeded(ctx context.Context, q queryer) (bool, error) {
	if GenerationTwoSchemaHash() != PinnedGenerationTwoSchemaHash {
		return false, errors.New("generation-two schema hash drift")
	}
	expected, err := expectedGenerationTwoManifestHash()
	if err != nil {
		return false, err
	}
	actual, err := readGenerationManifest(ctx, q)
	if err != nil {
		return false, err
	}
	switch generationManifestDigest(actual) {
	case expected:
		return false, nil
	case preRoutingManifestHash, preKeyLimitsManifestHash, preResponseStartsManifestHash, preBetaTwoManifestHash, preBrowseManifestHash, preQuotaCleanupManifestHash, preStewardHoldReadManifestHash, preModelTokenReserveManifestHash, preHourlyQuotaManifestHash, preEmbeddingManifestHash, preBetaFourManifestHash, preRCOneManifestHash, preBlackjackManifestHash:
		return true, nil
	default:
		return false, errors.New("generation-two schema manifest mismatch")
	}
}

func extendKnownGenerationTwoSchema(ctx context.Context, database *sql.DB) (result error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Schema editing is connection-local. Clear it even if cancellation rolls
	// back the transaction before the interval extension can reset it itself.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.ExecContext(cleanup, `PRAGMA writable_schema=RESET`)
		result = errors.Join(result, err)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	needed, err := generationTwoExtensionNeeded(ctx, tx)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	digest := generationManifestDigest(manifest)
	if digest != preBlackjackManifestHash {
		if digest != preRCOneManifestHash {
			if digest != preBetaFourManifestHash {
				if digest != preModelTokenReserveManifestHash && digest != preHourlyQuotaManifestHash && digest != preEmbeddingManifestHash {
					// Extend any pre-beta.1 structure to the complete beta.1 schema first.
					if digest == preRoutingManifestHash || digest == preKeyLimitsManifestHash || digest == preResponseStartsManifestHash {
						if digest == preRoutingManifestHash {
							if _, err := tx.ExecContext(ctx, charityModelRoutingSchema); err != nil {
								return err
							}
						}
						if digest != preResponseStartsManifestHash {
							if _, err := tx.ExecContext(ctx, endpointKeyLimitsSchema); err != nil {
								return err
							}
						}
						if _, err := tx.ExecContext(ctx, dispatchResponseStartsSchema); err != nil {
							return err
						}
					}
					if digest != preBrowseManifestHash && digest != preQuotaCleanupManifestHash && digest != preStewardHoldReadManifestHash {
						// Prior schemas have no recurring-limit or presentation sidecars.
						if _, err := tx.ExecContext(ctx, betaTwoAdditiveSchema); err != nil {
							return err
						}
						if err := migrateBetaTwoDefaults(ctx, tx); err != nil {
							return err
						}
					}
					if digest != preQuotaCleanupManifestHash && digest != preStewardHoldReadManifestHash {
						if _, err := tx.ExecContext(ctx, browseIndexesSchema); err != nil {
							return err
						}
					}
					if digest != preStewardHoldReadManifestHash {
						if _, err := tx.ExecContext(ctx, quotaCleanupIndexesSchema); err != nil {
							return err
						}
					}
					if _, err := tx.ExecContext(ctx, stewardHoldReadSchema); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, fishingLengthSchema); err != nil {
						return err
					}
					if err := migrateFishingLengthFacts(ctx, tx); err != nil {
						return err
					}
				}
				if digest != preHourlyQuotaManifestHash && digest != preEmbeddingManifestHash {
					if _, err := tx.ExecContext(ctx, charityModelReserveSchema); err != nil {
						return err
					}
				}
				if err := extendHourlyQuotaInterval(ctx, tx); err != nil {
					return err
				}
				if err := extendEmbeddingRoutes(ctx, tx); err != nil {
					return err
				}
				manifest, err := readGenerationManifest(ctx, tx)
				if err != nil {
					return err
				}
				if generationManifestDigest(manifest) != preBetaFourManifestHash {
					return errors.New("prior schema extension did not reach the asset baseline")
				}
			}
			if err := extendDualAssetSchema(ctx, tx); err != nil {
				return err
			}
		}
		if err := ApplyDuelExtension(ctx, tx); err != nil {
			return err
		}
	}
	if err := applyBlackjackExtension(ctx, tx); err != nil {
		return err
	}
	if err := validateGenerationTwoManifest(ctx, tx); err != nil {
		return err
	}
	if err := foreignKeyCheck(ctx, tx); err != nil {
		return err
	}
	if err := extensionIntegrityCheck(ctx, tx); err != nil {
		return err
	}
	if err := ValidateAssetLedger(ctx, tx); err != nil {
		return err
	}
	if err := validateAssetCapacity(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateBetaTwoDefaults seeds the beta.2 sidecar rows required for every
// existing donation, charity model and the quota capacity singleton. The
// statements are idempotent so a second startup run is a no-op.
func migrateBetaTwoDefaults(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO charity_model_access(model_id, allowed_level_mask, public_description)
SELECT id, 31, '' FROM charity_models
WHERE id NOT IN (SELECT model_id FROM charity_model_access);`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO donation_handling(donation_id, state, revision, processed_at, processed_by_user_id, processed_by_role, closed_at, closed_reason, created_at, updated_at)
SELECT id, 'legacy', 1, NULL, NULL, '', NULL, '', created_at, updated_at FROM donations
WHERE id NOT IN (SELECT donation_id FROM donation_handling);`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO donation_quota_capacity(id, rows_used, rows_held) VALUES(1, 0, 0);`); err != nil {
		return err
	}
	return nil
}
