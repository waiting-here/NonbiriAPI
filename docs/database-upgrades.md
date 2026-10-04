# Database upgrades

Every stable v1.x release must accept any earlier stable v1.x database, starting
with v1.0.0, without requiring intermediate binaries. Alpha, beta and release
candidates are outside that long-term guarantee. During rc.6, the final rc.5
schema and registered rc.6 schemas have explicit import bridges.

## Schema and data versions

`internal/db/generation_two.sql` describes a new database at the current version.
It contains the final object definitions and initial fixed rows, without replaying
historical alterations. Fresh configuration comes from the current seed helpers.

`schema_state.version` identifies the installed schema **and data** revision.
`storageSchema` in `internal/db/schema_extension.go` registers consecutive forward
steps, each with its resulting structural manifest and SQL or a Go transaction
callback. A data-only conversion also advances the version. SQLite's
`application_id` and `user_version` continue to identify the storage generation.

For a persistent change:

1. Append a migration that transforms the previous revision, including new
   configuration keys and fixed rows. Preserve existing operator choices. Keep
   released migration SQL and conversion behavior immutable; do not call mutable
   fresh-default helpers from an old step.
2. Update the fresh DDL and seeds to produce the same target. Update the pinned
   schema hash, structural manifest and configuration golden where applicable.
3. Test direct upgrades from every supported schema/data revision. Include
   populated accounts, credentials, game saves and receipts; cover failure,
   cancellation, retry and repeated startup. No-change releases can reuse the
   same database revision and fixtures.

Startup checks source identity and its registered manifest before writes. All
missing steps run in one transaction; target schema, required configuration,
fixed identities and credentials are checked before commit. Unknown versions or
structural drift reject. Failed or cancelled migrations roll back the whole
chain. Full historical audits remain an explicit maintenance operation on a
stopped database or an upgraded consistent copy.

At v1.0.0 publication, freeze its exact baseline and retain its populated fixture
as the oldest stable source. Later releases append to that chain. Prerelease
imports live separately in `schema_prerelease.go` and `migrations/pre_release/`;
they can be retired when the stable baseline is frozen. The current multi-step
unit matrix uses synthetic revisions; it does not claim to test future releases.
`scripts/check-upgrade.sh` additionally builds the pinned released predecessor to
generate and upgrade its own populated fixture.

## Games and activities

Keep a player's durable profile separate from activity periods, participation
receipts and live rounds. Add tables and conversions through the central upgrade
chain; use the existing [game module](game-modules.md) and lifecycle contracts for
runtime integration, export, deletion and retention.

Version a save's encoding separately from its gameplay rules. Lake Notes profiles
and cast checkpoints have explicit storage versions. Compatible catalog changes
can read a validated profile without resetting it; an unfinished cast still
requires its exact simulation rules, saved random state and settlement facts.
An incompatible format needs a conversion that preserves those facts or a retained
reader until its data is converted or expires. Closing an entry point does not
retire data, entitlements, idempotency receipts or lifecycle obligations.

Keep only the historical execution paths still needed by retained work. Before
removing one, prove that no supported source or retained record needs it, or add
an explicit migration. Database rollback always restores a complete matching
stopped snapshot; an older binary alone is not a downgrade procedure.
