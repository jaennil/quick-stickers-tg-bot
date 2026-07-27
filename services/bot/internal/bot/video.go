package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) handleVideo(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	video := update.Message.Video
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	logger.Log.Infow("[VIDEO] received",
		"video", video.FileUniqueID,
		"user", userID,
		"duration", video.Duration,
		"width", video.Width,
		"height", video.Height,
	)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Ищу текст в кадрах видео...",
	})
	if err != nil {
		logger.Log.Errorw("Error sending progress message", "error", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: video.FileID})
	if err != nil {
		logger.Log.Errorw("[VIDEO] failed to get file", "video", video.FileUniqueID, "error", err)
		b.editVideoError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось скачать видео.")
		return
	}

	fileURL := tgBot.FileDownloadLink(file)
	text, ocrErr := b.indexer.DownloadAndOCRWithType(ctx, fileURL, service.StickerTypeVideo)
	if ocrErr != nil {
		logger.Log.Warnw("[OCR] video recognition failed",
			"video", video.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"error", ocrErr,
		)
	} else if text != "" {
		logger.Log.Infow("[OCR] video result",
			"video", video.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"text", text,
		)
	}

	documentID, _ := fileid.DecodeDocumentID(video.FileID)
	media := &repository.Sticker{
		UserID:     userID,
		StickerID:  video.FileUniqueID,
		FileID:     video.FileID,
		DocumentID: documentID,
		Text:       text,
		MediaType:  repository.MediaTypeVideo,
	}
	if ocrErr == nil {
		media.OCREngine = constants.DefaultOCREngine.Name
	}
	if err := b.repo.SaveSticker(media); err != nil {
		logger.Log.Errorw("[VIDEO] failed to save", "video", video.FileUniqueID, "user", userID, "error", err)
		b.editVideoError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось сохранить видео в базе.")
		return
	}
	b.state.SetLastSticker(userID, video.FileUniqueID)

	go func() {
		thumbURL := fileURL
		thumbType := service.StickerTypeVideo
		if video.Thumbnail != nil {
			thumbFile, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: video.Thumbnail.FileID})
			if err == nil {
				thumbURL = tgBot.FileDownloadLink(thumbFile)
				thumbType = service.StickerTypeStatic
			}
		}
		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, video.FileID, thumbURL, thumbType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "video", video.FileUniqueID, "error", err)
		}
	}()

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      buildOCRResultMessage("видео", text, ocrErr),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(video.FileUniqueID)},
		},
	})
}

func (b *Bot) editVideoError(ctx context.Context, tgBot *bot.Bot, chatID int64, messageID int, text string) {
	if _, err := tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: chatID, MessageID: messageID, Text: text,
	}); err != nil {
		logger.Log.Errorw("[VIDEO] failed to edit error message", "error", err)
	}
}
