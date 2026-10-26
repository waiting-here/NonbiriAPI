package aiagent

import (
	"context"
	"testing"
)

func TestAutomaticReviewModeDowngradeCannotBeReversed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	settings := defaultSettings()
	settings.Mode = "auto"
	var err error
	settings, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,created_at,updated_at) VALUES('auto_review',1,'admin','reviewer',10,'v1','auto','running',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := result.LastInsertId()
	if _, err = f.db.Exec(`INSERT INTO ai_agent_review_events(donation_id,sequence,mode,created_at,updated_at) VALUES(10,1,'auto',0,0)`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"preview", "auto"} {
		settings.Mode = mode
		settings, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision})
		if err != nil {
			t.Fatal(err)
		}
	}
	var eventMode string
	var sequence int64
	if err = f.db.QueryRow(`SELECT mode,sequence FROM ai_agent_review_events WHERE donation_id=10`).Scan(&eventMode, &sequence); err != nil || eventMode != "preview" || sequence != 2 {
		t.Fatal("queued event regained execution permission", eventMode, sequence, err)
	}
	job, actor, err := f.s.loadJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.finishReview(ctx, job, actor, ReviewResult{Decision: "reject", InternalReason: "missing information", UserNote: "Please add details"}); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "test_mutations") != 0 || len(f.ops.prepared) != 0 {
		t.Fatal("downgraded review executed")
	}
	if err = f.s.collectReviewEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = f.db.QueryRow(`SELECT mode FROM ai_agent_jobs WHERE id>?`, jobID).Scan(&mode); err != nil || mode != "preview" {
		t.Fatal("queued event lost preview restriction", mode, err)
	}
}
