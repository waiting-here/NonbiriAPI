package aiagent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestContextReadBoundsFailedHistoryAndPreservesArchive(t *testing.T) {
	f := newFixture(t)
	job := f.send(t, strings.Repeat("a", 512), "h")
	if _, err := f.db.Exec(`INSERT INTO ai_agent_messages(conversation_id,generation,role,content,created_at) VALUES(?,?,'user','latest',?)`, job.ConversationID, job.Generation, f.s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = readContextHistory(context.Background(), tx, job.ConversationID, 0, 256); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded context: %v", err)
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM ai_agent_messages WHERE conversation_id=?`, job.ConversationID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history changed: %d, %v", count, err)
	}
	all, err := readContextHistory(context.Background(), tx, job.ConversationID, 0, 4096)
	if err != nil || len(all) != 2 {
		t.Fatalf("read within budget: %d, %v", len(all), err)
	}
	tail, err := readContextHistory(context.Background(), tx, job.ConversationID, all[0].ID, 256)
	if err != nil || len(tail) != 1 || tail[0].Content != "latest" {
		t.Fatalf("summarized history counted toward active context: %+v, %v", tail, err)
	}
}
