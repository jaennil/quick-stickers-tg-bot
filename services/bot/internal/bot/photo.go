package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) handlePhoto(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	photos := update.Message.Photo
	if len(photos) == 0 {
		return
	}

	photo := photos[len(photos)-1]
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	logger.Log.Infow("[PHOTO] received",
		"photo", photo.FileUniqueID,
		"user", userID,
		"width", photo.Width,
		"height", photo.Height,
	)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Распознаю текст на картинке...",
	})
	if err != nil {
		logger.Log.Errorw("Error sending progress message", "error", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: photo.FileID})
	if err != nil {
		logger.Log.Errorw("Error getting file", "error", err)
		return
	}

	fileURL := tgBot.FileDownloadLink(file)

	photoType := service.StickerTypeStatic

	text, ocrErr := b.indexer.DownloadAndOCRWithType(ctx, fileURL, photoType)
	if ocrErr != nil {
		logger.Log.Warnw("[OCR] photo recognition failed",
			"photo", photo.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"error", ocrErr,
		)
	} else if text != "" {
		logger.Log.Infow("[OCR] result",
			"photo", photo.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"text", text,
		)
	}

	p := &repository.Sticker{
		UserID:    userID,
		StickerID: photo.FileUniqueID,
		SetName:   "",
		FileID:    photo.FileID,
		Text:      text,
		Emoji:     "",
		MediaType: repository.MediaTypePhoto,
	}
	if ocrErr == nil {
		p.OCREngine = constants.DefaultOCREngine.Name
	}
	if err := b.repo.SaveSticker(p); err != nil {
		logger.Log.Errorw("[PHOTO] failed to save photo", "photo", photo.FileUniqueID, "user", userID, "error", err)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      "⚠️ Не удалось сохранить картинку в базе.",
		})
		return
	}
	b.state.SetLastSticker(userID, photo.FileUniqueID)

	go func() {
		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, photo.FileID, fileURL, photoType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "photo", photo.FileUniqueID, "error", err)
		}
	}()

	buttons := [][]models.InlineKeyboardButton{ui.EditStickerButton(photo.FileUniqueID)}

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      buildOCRResultMessage("картинку", text, ocrErr),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}
