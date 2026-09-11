package semantic

import (
	"context"
	"path/filepath"
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

func (f *fakeEmbedder) EmbedMedia(context.Context, string, []byte, string, bool) ([]float32, error) {
	return f.query, nil
}

func (f *fakeEmbedder) Model() string { return "test-model" }

func TestSearchCombinesExactAndSemanticResults(t *testing.T) {
	repo, err := sqlite.New(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	items := []*repository.Sticker{
		{UserID: 1, StickerID: "exact", FileID: "file-1", Text: "кот грустит"},
		{UserID: 1, StickerID: "semantic", FileID: "file-2", Text: "печальный зверь"},
		{UserID: 1, StickerID: "other", FileID: "file-3", Text: "весёлый самолёт"},
	}
	for _, item := range items {
		if err := repo.SaveSticker(item); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveEmbedding(1, "exact", "test-model", items[0].Text, items[0].FileID, encodeVector([]float32{0.9, 0.1})); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEmbedding(1, "semantic", "test-model", items[1].Text, items[1].FileID, encodeVector([]float32{1, 0})); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEmbedding(1, "other", "test-model", items[2].Text, items[2].FileID, encodeVector([]float32{0, 1})); err != nil {
		t.Fatal(err)
	}

	service := New(repo, &fakeEmbedder{query: []float32{1, 0}}, "", "", 0.25, 0)
	result, err := service.Search(context.Background(), 1, "кот грустит")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].StickerID != "exact" || result[1].StickerID != "semantic" {
		t.Fatalf("unexpected ranking: %s, %s", result[0].StickerID, result[1].StickerID)
	}

	candidates, err := repo.GetEmbeddingCandidates("test-model", 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unexpected initial candidates: %d, %v", len(candidates), err)
	}
	if err := repo.UpdateStickerText(1, "exact", "new text"); err != nil {
		t.Fatal(err)
	}
	candidates, err = repo.GetEmbeddingCandidates("test-model", 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expected changed sticker to need indexing: %d, %v", len(candidates), err)
	}
}
