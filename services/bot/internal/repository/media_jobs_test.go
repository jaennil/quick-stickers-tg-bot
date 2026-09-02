package repository

import (
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func newQueueTestRepository(t *testing.T) *BaseRepository {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE media_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			progress_message_id INTEGER NOT NULL DEFAULT 0,
			sticker_id TEXT NOT NULL,
			file_id TEXT NOT NULL,
			media_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, sticker_id)
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	return &BaseRepository{db: db}
}

func TestMediaJobSurvivesRetryAndRestart(t *testing.T) {
	repo := newQueueTestRepository(t)
	job := &MediaJob{
		UserID: 42, ChatID: 84, StickerID: "photo-1", FileID: "file-1", MediaType: MediaTypePhoto,
	}
	if err := repo.EnqueueMediaJob(job); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateMediaJobProgressMessage(42, "photo-1", 123); err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.ClaimNextMediaJob()
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ProgressMessageID != 123 || claimed.Attempts != 1 {
		t.Fatalf("unexpected claimed job: %#v", claimed)
	}

	if err := repo.RequeueProcessingMediaJobs(); err != nil {
		t.Fatal(err)
	}
	restored, err := repo.ClaimNextMediaJob()
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || restored.ID != claimed.ID || restored.Attempts != 2 {
		t.Fatalf("unexpected restored job: %#v", restored)
	}
	if err := repo.CompleteMediaJob(restored.ID); err != nil {
		t.Fatal(err)
	}
	empty, err := repo.ClaimNextMediaJob()
	if err != nil || empty != nil {
		t.Fatalf("expected empty queue, got job=%#v err=%v", empty, err)
	}
}

func TestRetriedMediaJobDoesNotBlockNewerJobs(t *testing.T) {
	repo := newQueueTestRepository(t)
	for _, id := range []string{"first", "second"} {
		if err := repo.EnqueueMediaJob(&MediaJob{
			UserID: 1, ChatID: 1, StickerID: id, FileID: id, MediaType: MediaTypePhoto,
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := repo.ClaimNextMediaJob()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RetryMediaJob(first.ID, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	second, err := repo.ClaimNextMediaJob()
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.StickerID != "second" {
		t.Fatalf("expected newer untried job, got %#v", second)
	}
}
