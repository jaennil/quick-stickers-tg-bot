package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite"
)

type fakeEmbedder struct {
	query []float32
}

func (f *fakeEmbedder) EmbedText(context.Context, string) ([]float32, error) {
	return f.query, nil
}

func (f *fakeEmbedder) Model() string { return "test-model" }

type fakeDescriber struct {
	calls int
	fail  bool
}

func (f *fakeDescriber) Describe(context.Context, []byte, string) (string, error) {
	f.calls++
	if f.fail {
		return "", errors.New("vision down")
	}
	return "распознанный текст\nна картинке кот", nil
}

func (f *fakeDescriber) VisionModel() string { return "test-vision" }

// goose panics if migrations are registered twice in one process, so the whole
// package shares a single database and each test uses its own user id.
var (
	sharedRepo *repository.BaseRepository
	repoOnce   sync.Once
	nextUserID atomic.Int64
)

func newTestRepo(t *testing.T) (*repository.BaseRepository, int64) {
	t.Helper()
	repoOnce.Do(func() {
		dir, err := os.MkdirTemp("", "semantic-test")
		if err != nil {
			panic(err)
		}
		sharedRepo, err = sqlite.New(filepath.Join(dir, "search.db"))
		if err != nil {
			panic(err)
		}
	})
	return sharedRepo, nextUserID.Add(1)
}

// Candidate queries scan every user, so tests filter down to their own rows.
func onlyUser(list []*repository.Sticker, userID int64) []*repository.Sticker {
	var out []*repository.Sticker
	for _, s := range list {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out
}

func TestSearchCombinesExactAndSemanticResults(t *testing.T) {
	repo, uid := newTestRepo(t)

	items := []*repository.Sticker{
		{UserID: uid, StickerID: "exact", FileID: "file-1", Text: "кот грустит"},
		{UserID: uid, StickerID: "semantic", FileID: "file-2", Text: "печальный зверь"},
		{UserID: uid, StickerID: "other", FileID: "file-3", Text: "весёлый самолёт"},
	}
	for _, item := range items {
		if err := repo.SaveSticker(item); err != nil {
			t.Fatal(err)
		}
	}
	vectors := map[string][]float32{
		"exact": {0.9, 0.1}, "semantic": {1, 0}, "other": {0, 1},
	}
	for _, item := range items {
		if err := repo.SaveEmbedding(uid, item.StickerID, "test-model",
			item.Text, item.FileID, encodeVector(vectors[item.StickerID])); err != nil {
			t.Fatal(err)
		}
	}

	service := New(repo, &fakeEmbedder{query: []float32{1, 0}}, nil, 0.25, 0)
	result, err := service.Search(context.Background(), uid, "кот грустит")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].StickerID != "exact" || result[1].StickerID != "semantic" {
		t.Fatalf("unexpected ranking: %s, %s", result[0].StickerID, result[1].StickerID)
	}
}

// The vision call is the only expensive part of indexing, so it must survive a
// crash that happens before the embedding is written.
func TestVisionWorkSurvivesInterruptedIndexing(t *testing.T) {
	repo, uid := newTestRepo(t)
	if err := repo.SaveSticker(&repository.Sticker{
		UserID: uid, StickerID: "s1", FileID: "file-1", Text: "ocr",
	}); err != nil {
		t.Fatal(err)
	}

	pendingAll, err := repo.GetAITextCandidates("test-vision", 100)
	pending := onlyUser(pendingAll, uid)
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected one vision candidate: %d, %v", len(pending), err)
	}
	// Nothing to embed yet: the embedding stage must not run ahead of vision.
	toEmbedAll, err := repo.GetEmbeddingCandidates("test-model", 100)
	toEmbed := onlyUser(toEmbedAll, uid)
	if err != nil || len(toEmbed) != 0 {
		t.Fatalf("expected no embedding candidates before vision: %d, %v", len(toEmbed), err)
	}

	// Vision succeeds and is committed; the process then "dies".
	if err := repo.SaveAIText(uid, "s1", "test-vision", "распознано\nкот", "file-1"); err != nil {
		t.Fatal(err)
	}

	// After restart the vision stage must consider it done...
	pendingAll, err = repo.GetAITextCandidates("test-vision", 100)
	pending = onlyUser(pendingAll, uid)
	if err != nil || len(pending) != 0 {
		t.Fatalf("vision work was lost, would be paid for twice: %d, %v", len(pending), err)
	}
	// ...while the embedding stage picks up exactly where it stopped.
	toEmbedAll, err = repo.GetEmbeddingCandidates("test-model", 100)
	toEmbed = onlyUser(toEmbedAll, uid)
	if err != nil || len(toEmbed) != 1 {
		t.Fatalf("expected pending embedding after restart: %d, %v", len(toEmbed), err)
	}
	if toEmbed[0].AIText != "распознано\nкот" {
		t.Fatalf("embedding stage got wrong text: %q", toEmbed[0].AIText)
	}

	if err := repo.SaveEmbedding(uid, "s1", "test-model",
		toEmbed[0].AIText, "file-1", encodeVector([]float32{1, 0})); err != nil {
		t.Fatal(err)
	}
	toEmbedAll, err = repo.GetEmbeddingCandidates("test-model", 100)
	toEmbed = onlyUser(toEmbedAll, uid)
	if err != nil || len(toEmbed) != 0 {
		t.Fatalf("expected nothing left to embed: %d, %v", len(toEmbed), err)
	}
}

// A media whose text changes must be re-embedded, but must not be re-described:
// the picture it was read from has not changed.
func TestChangedTextReembedsWithoutNewVisionCall(t *testing.T) {
	repo, uid := newTestRepo(t)
	if err := repo.SaveSticker(&repository.Sticker{
		UserID: uid, StickerID: "s1", FileID: "file-1", Text: "ocr",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAIText(uid, "s1", "test-vision", "первый вариант", "file-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEmbedding(uid, "s1", "test-model", "первый вариант", "file-1",
		encodeVector([]float32{1, 0})); err != nil {
		t.Fatal(err)
	}

	if err := repo.SaveAIText(uid, "s1", "test-vision", "второй вариант", "file-1"); err != nil {
		t.Fatal(err)
	}
	toEmbedAll, err := repo.GetEmbeddingCandidates("test-model", 100)
	toEmbed := onlyUser(toEmbedAll, uid)
	if err != nil || len(toEmbed) != 1 {
		t.Fatalf("expected re-embedding after text change: %d, %v", len(toEmbed), err)
	}
	pendingAll, err := repo.GetAITextCandidates("test-vision", 100)
	pending := onlyUser(pendingAll, uid)
	if err != nil || len(pending) != 0 {
		t.Fatalf("must not pay for vision again: %d, %v", len(pending), err)
	}
}

// A failing vision call must not wedge the queue behind it.
func TestFailedVisionIsRetriedButDoesNotBlockQueue(t *testing.T) {
	repo, uid := newTestRepo(t)
	for _, id := range []string{"s1", "s2"} {
		if err := repo.SaveSticker(&repository.Sticker{
			UserID: uid, StickerID: id, FileID: "file-" + id, Text: "ocr",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.MarkAITextAttempt(uid, "s1"); err != nil {
		t.Fatal(err)
	}
	pendingAll, err := repo.GetAITextCandidates("test-vision", 100)
	pending := onlyUser(pendingAll, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].StickerID != "s2" {
		t.Fatalf("attempted media must move to the back of the queue, got %v", pending)
	}
}
