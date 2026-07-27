package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

func sendStoredMedia(ctx context.Context, tgBot *bot.Bot, chatID int64, media *repository.Sticker) error {
	file := &models.InputFileString{Data: media.FileID}
	switch media.MediaType {
	case repository.MediaTypePhoto:
		_, err := tgBot.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: file})
		return err
	case repository.MediaTypeVideo:
		_, err := tgBot.SendVideo(ctx, &bot.SendVideoParams{ChatID: chatID, Video: file})
		return err
	default:
		_, err := tgBot.SendSticker(ctx, &bot.SendStickerParams{ChatID: chatID, Sticker: file})
		return err
	}
}

func cachedInlineMedia(index int, media *repository.Sticker) models.InlineQueryResult {
	id := fmt.Sprintf("%d_%s", index, media.StickerID)
	switch media.MediaType {
	case repository.MediaTypePhoto:
		return &models.InlineQueryResultCachedPhoto{ID: id, PhotoFileID: media.FileID}
	case repository.MediaTypeVideo:
		title := media.Text
		if title == "" {
			title = "Видео"
		}
		return &models.InlineQueryResultCachedVideo{ID: id, VideoFileID: media.FileID, Title: title}
	default:
		return &models.InlineQueryResultCachedSticker{ID: id, StickerFileID: media.FileID}
	}
}
