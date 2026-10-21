package v1

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestConversionAuditsRebuiltCredentialsAndPreservesUnrelatedHistory(t *testing.T) {
	for _, fault := range []string{"credential", "foreign-key"} {
		t.Run(fault, func(t *testing.T) {
			path, vault := sourceFixtureFromDDL(t, schema21SQLForTest(t), 21, stableSchema21Manifest)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "credential":
				mustExec(t, database, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://synthetic.example/v1','openai-compatible','invalid-ciphertext',1)`, hostileBlob16(5))
			case "foreign-key":
				mustExec(t, database, "PRAGMA foreign_keys=OFF; INSERT INTO account_mfa_attempts(user_id,window_start,failures,blocked_until) VALUES(999999,1,1,0)")
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(t.Context(), path, vault, Options{}); err == nil {
				t.Fatal("explicit source audit missed unrelated damage")
			}
			var events []Progress
			result, err := Run(t.Context(), path, vault, Options{Apply: true, Progress: func(p Progress) { events = append(events, p) }})
			if fault == "credential" {
				if err == nil {
					t.Fatal("rebuilt credentials were not audited")
				}
				database, openErr := sql.Open("sqlite", path)
				if openErr != nil {
					t.Fatal(openErr)
				}
				plan, planErr := storageSchema.plan(t.Context(), database)
				if closeErr := database.Close(); planErr != nil || closeErr != nil || len(plan.steps) != 8 || plan.steps[0].from != 21 {
					t.Fatal("credential rejection changed the exact source", planErr, closeErr)
				}
				return
			}
			if err != nil || result.Status != "converted" {
				t.Fatal("conversion rejected unrelated history", result, err)
			}
			var credentialAudit, endpointRebuild, endpointRelations bool
			for _, event := range events {
				credentialAudit = credentialAudit || event.Phase == "audit" && event.Table == "endpoint_key_secrets"
				endpointRebuild = endpointRebuild || event.Phase == "rebuild" && event.Table == "endpoints"
				endpointRelations = endpointRelations || event.Phase == "relationships" && event.Table == "endpoint_keys"
				if event.Phase == "relationships" && event.Table == "account_mfa_attempts" {
					t.Fatal("conversion scanned an unrelated historical relationship", event)
				}
			}
			if !credentialAudit || !endpointRebuild || !endpointRelations {
				t.Fatal("conversion omitted affected checks", credentialAudit, endpointRebuild, endpointRelations)
			}
			if _, err := Run(t.Context(), path, vault, Options{}); err == nil {
				t.Fatal("explicit current audit missed preserved damage")
			}
			if result, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil || result.Status != "current" {
				t.Fatal("current trusted reentry failed", result, err)
			}
		})
	}
}

func TestRebuiltSourceRetainsCredentialAndAffectedRelationChecks(t *testing.T) {
	for _, fault := range []string{"credential", "outgoing", "incoming", "composite"} {
		t.Run(fault, func(t *testing.T) {
			path, vault, _ := stableSourceFixture(t)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			// Remove all trigger enforcement only for constructing a damaged
			// source, then restore its exact published schema identity.
			rows, err := database.Query("SELECT name,sql FROM sqlite_schema WHERE type='trigger'")
			if err != nil {
				t.Fatal(err)
			}
			triggers := map[string]string{}
			for rows.Next() {
				var name, ddl string
				if err := rows.Scan(&name, &ddl); err != nil {
					t.Fatal(err)
				}
				triggers[name] = ddl
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
			for name := range triggers {
				mustExec(t, database, "DROP TRIGGER "+quoteSQLiteIdentifier(name))
			}
			mustExec(t, database, "PRAGMA foreign_keys=OFF")
			switch fault {
			case "credential":
				mustExec(t, database, "UPDATE endpoint_key_secrets SET encrypted_secret='invalid-ciphertext'")
			case "outgoing":
				mustExec(t, database, "INSERT INTO endpoints(id,user_id,connector_type,base_url,created_at,updated_at) VALUES(9,999999,'openai-compatible','https://synthetic.example/v1',1,1)")
			case "incoming":
				mustExec(t, database, "INSERT INTO endpoint_keys(id,endpoint_id,secret_ref_id,secret_fingerprint,created_at,updated_at) VALUES(9,999999,1,?,1,1)", hostileBlob32(1))
			case "composite":
				mustExec(t, database, "INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,0,'999')", hostileOID("fb_"))
			}
			for _, ddl := range triggers {
				mustExec(t, database, ddl)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			var events []Progress
			if _, err := Run(t.Context(), path, vault, Options{Apply: true, Progress: func(p Progress) { events = append(events, p) }}); err == nil {
				t.Fatal("affected damage accepted")
			}
			database, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			plan, err := storageSchema.plan(t.Context(), database)
			if err != nil || len(plan.steps) != 13 || plan.steps[0].from != 17 || plan.steps[12].version != 30 {
				t.Fatal("failure did not preserve source", err)
			}
			if fault != "credential" {
				var rolledBack bool
				for _, event := range events {
					rolledBack = rolledBack || event.Phase == "rollback" && event.State == "completed"
				}
				if !rolledBack {
					t.Fatal("failed conversion did not confirm rollback")
				}
			}
		})
	}
}

func TestOfflineCancellationAtConversionBoundariesRestoresExactSource(t *testing.T) {
	for _, phase := range []string{"rebuild", "relationships", "target", "commit"} {
		t.Run(phase, func(t *testing.T) {
			path, vault := sourceFixtureFromDDL(t, schema20SQLForTest(t), 20, stableSchema20Manifest)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var rolledBack bool
			_, err := Run(ctx, path, vault, Options{Apply: true, Progress: func(event Progress) {
				if event.Phase == phase && event.State == "started" {
					cancel()
				}
				rolledBack = rolledBack || event.Phase == "rollback" && event.State == "completed"
			}})
			if !errors.Is(err, context.Canceled) || !rolledBack {
				t.Fatal("cancel did not finish rollback", rolledBack, err)
			}
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := storageSchema.plan(t.Context(), database)
			if err := errors.Join(err, database.Close()); err != nil || len(plan.steps) != 10 || plan.steps[0].from != 20 || plan.steps[9].version != 30 {
				t.Fatal("exact source not restored", err)
			}
			if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
				t.Fatal("retry after cancellation failed", err)
			}
		})
	}
}
