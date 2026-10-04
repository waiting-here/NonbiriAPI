package imageactivity

import (
	"errors"
	"testing"
)

func TestResultPublicationFollowsCommitAndBlocksEarlyReaders(t *testing.T) {
	m := memoryStore{items: map[string]*memoryItem{}}
	if !m.restoreExecution("task", 1, executionMemory) {
		t.Fatal("execution reservation failed")
	}
	images := []imageBytes{{data: []byte("image"), mime: "image/png"}}
	failed := errors.New("commit failed")
	if published, err := m.commitAndPublish(func() error { return failed }, "task", 1, images, 100); published || !errors.Is(err, failed) {
		t.Fatalf("failed commit published=%v err=%v", published, err)
	}
	if len(m.metadata("task", 1, 1)) != 0 {
		t.Fatal("failed commit exposed images")
	}

	readStarted := make(chan struct{})
	readResult := make(chan []ImageInfo, 1)
	published, err := m.commitAndPublish(func() error {
		// SQL can make the success row visible before Commit returns.
		go func() {
			close(readStarted)
			readResult <- m.metadata("task", 1, 1)
		}()
		<-readStarted
		if m.mu.TryLock() {
			m.mu.Unlock()
			t.Error("result reads were not held through commit")
		}
		return nil
	}, "task", 1, images, 100)
	if !published || err != nil {
		t.Fatalf("successful publication=%v err=%v", published, err)
	}
	if got := <-readResult; len(got) != 1 || got[0].Bytes != len(images[0].data) {
		t.Fatalf("first committed read: %+v", got)
	}
}
