package bot

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) handleAnimation(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	animation := update.Message.Animation
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	logger.Log.Infow("[GIF] received",
		"gif", animation.FileUniqueID,
		"user", userID,
		"duration", animation.Duration,
		"width", animation.Width,
		"height", animation.Height,
		"mime_type", animation.MimeType,
	)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Ищу текст в кадрах GIF...",
	})
	if err != nil {
		logger.Log.Errorw("[GIF] failed to send progress", "error", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: animation.FileID})
	if err != nil {
		logger.Log.Errorw("[GIF] failed to get file", "gif", animation.FileUniqueID, "error", err)
		b.editAnimationError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось скачать GIF.")
		return
	}

	fileURL := tgBot.FileDownloadLink(file)
	text, ocrErr := b.indexer.DownloadAndOCRWithType(ctx, fileURL, service.StickerTypeVideo)
	if ocrErr != nil {
		logger.Log.Warnw("[OCR] GIF recognition failed", "gif", animation.FileUniqueID, "error", ocrErr)
	} else if text != "" {
		logger.Log.Infow("[OCR] GIF result", "gif", animation.FileUniqueID, "text", text)
	}

	documentID, _ := fileid.DecodeDocumentID(animation.FileID)
	media := &repository.Sticker{
		UserID:     userID,
		StickerID:  animation.FileUniqueID,
		FileID:     animation.FileID,
		DocumentID: documentID,
		Text:       text,
		IsAnimated: true,
		IsVideo:    animationUsesVideoContainer(animation.MimeType, animation.FileName),
		MediaType:  repository.MediaTypeGIF,
	}
	if ocrErr == nil {
		media.OCREngine = constants.DefaultOCREngine.Name
	}
	if err := b.repo.SaveSticker(media); err != nil {
		logger.Log.Errorw("[GIF] failed to save", "gif", animation.FileUniqueID, "user", userID, "error", err)
		b.editAnimationError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось сохранить GIF в базе.")
		return
	}
	b.state.SetLastSticker(userID, animation.FileUniqueID)

	go func() {
		thumbURL := fileURL
		thumbType := service.StickerTypeVideo
		if animation.Thumbnail != nil {
			thumbFile, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: animation.Thumbnail.FileID})
			if err == nil {
				thumbURL = tgBot.FileDownloadLink(thumbFile)
				thumbType = service.StickerTypeStatic
			}
		}
		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, animation.FileID, thumbURL, thumbType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "gif", animation.FileUniqueID, "error", err)
		}
	}()

	_, _ = tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      buildOCRResultMessage("GIF", text, ocrErr),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(animation.FileUniqueID)},
		},
	})
}

func animationUsesVideoContainer(mimeType, fileName string) bool {
	return strings.HasPrefix(strings.ToLower(mimeType), "video/") ||
		strings.HasSuffix(strings.ToLower(fileName), ".mp4")
}

func (b *Bot) editAnimationError(ctx context.Context, tgBot *bot.Bot, chatID int64, messageID int, text string) {
	if _, err := tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: chatID, MessageID: messageID, Text: text,
	}); err != nil {
		logger.Log.Errorw("[GIF] failed to edit error message", "error", err)
	}
}
