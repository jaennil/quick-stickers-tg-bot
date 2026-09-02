package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/jaennil/sticker-search-bot/internal/repository"
)

func TestMigrationsCreateDurableMediaQueue(t *testing.T) {
	repo, err := New(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	if err := repo.EnqueueMediaJob(&repository.MediaJob{
		UserID: 1, ChatID: 2, StickerID: "photo", FileID: "file", MediaType: repository.MediaTypePhoto,
	}); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimNextMediaJob()
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.StickerID != "photo" {
		t.Fatalf("unexpected migrated queue result: %#v", job)
	}
}
