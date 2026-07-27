package bot

import (
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

func TestCachedInlineMediaVideo(t *testing.T) {
	result := cachedInlineMedia(2, &repository.Sticker{
		StickerID: "video-unique-id",
		FileID:    "video-file-id",
		Text:      "распознанный текст",
		MediaType: repository.MediaTypeVideo,
	})

	video, ok := result.(*models.InlineQueryResultCachedVideo)
	if !ok {
		t.Fatalf("expected cached video, got %T", result)
	}
	if video.VideoFileID != "video-file-id" {
		t.Fatalf("unexpected file id: %q", video.VideoFileID)
	}
	if video.Title != "распознанный текст" {
		t.Fatalf("unexpected title: %q", video.Title)
	}
}

func TestCachedInlineMediaVideoFallbackTitle(t *testing.T) {
	result := cachedInlineMedia(0, &repository.Sticker{
		StickerID: "video-unique-id",
		FileID:    "video-file-id",
		MediaType: repository.MediaTypeVideo,
	})

	video := result.(*models.InlineQueryResultCachedVideo)
	if video.Title != "Видео" {
		t.Fatalf("unexpected fallback title: %q", video.Title)
	}
}
