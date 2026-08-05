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

func TestCachedInlineMediaGIF(t *testing.T) {
	tests := []struct {
		name      string
		isVideo   bool
		wantMPEG4 bool
	}{
		{name: "native gif"},
		{name: "mpeg4 gif", isVideo: true, wantMPEG4: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cachedInlineMedia(1, &repository.Sticker{
				StickerID: "gif-unique-id",
				FileID:    "gif-file-id",
				Text:      "gif text",
				IsVideo:   tt.isVideo,
				MediaType: repository.MediaTypeGIF,
			})

			if tt.wantMPEG4 {
				gif, ok := result.(*models.InlineQueryResultCachedMpeg4Gif)
				if !ok || gif.Mpeg4FileID != "gif-file-id" || gif.Title != "gif text" {
					t.Fatalf("unexpected MPEG4 GIF result: %#v", result)
				}
				return
			}

			gif, ok := result.(*models.InlineQueryResultCachedGif)
			if !ok || gif.GifFileID != "gif-file-id" || gif.Title != "gif text" {
				t.Fatalf("unexpected GIF result: %#v", result)
			}
		})
	}
}
