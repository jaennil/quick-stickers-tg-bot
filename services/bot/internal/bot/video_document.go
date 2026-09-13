package bot

import (
	"context"
	"path/filepath"
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

func isVideoDocument(document *models.Document) bool {
	if document == nil {
		return false
	}
	if strings.HasPrefix(strings.ToLower(document.MimeType), "video/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(document.FileName)) {
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv":
		return true
	default:
		return false
	}
}

func (b *Bot) handleVideoDocument(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	document := update.Message.Document
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	logger.Log.Infow("[VIDEO_FILE] received",
		"video", document.FileUniqueID,
		"user", userID,
		"file_name", document.FileName,
		"mime_type", document.MimeType,
		"size", document.FileSize,
	)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Ищу текст в кадрах видео-файла...",
	})
	if err != nil {
		logger.Log.Errorw("[VIDEO_FILE] failed to send progress", "error", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: document.FileID})
	if err != nil {
		logger.Log.Errorw("[VIDEO_FILE] failed to get file", "video", document.FileUniqueID, "error", err)
		b.editVideoError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось скачать видео-файл.")
		return
	}

	fileURL := tgBot.FileDownloadLink(file)
	text, ocrErr := b.indexer.DownloadAndOCRWithType(ctx, fileURL, service.StickerTypeVideo)
	if ocrErr != nil {
		logger.Log.Warnw("[OCR] video file recognition failed",
			"video", document.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"error", ocrErr,
		)
	} else if text != "" {
		logger.Log.Infow("[OCR] video file result",
			"video", document.FileUniqueID,
			"engine", constants.DefaultOCREngine.Name,
			"text", text,
		)
	}

	documentID, _ := fileid.DecodeDocumentID(document.FileID)
	media := &repository.Sticker{
		UserID:     userID,
		StickerID:  document.FileUniqueID,
		FileID:     document.FileID,
		DocumentID: documentID,
		Text:       text,
		MediaType:  repository.MediaTypeVideoFile,
	}
	if ocrErr == nil {
		media.OCREngine = constants.DefaultOCREngine.Name
	}
	if err := b.repo.SaveSticker(media); err != nil {
		logger.Log.Errorw("[VIDEO_FILE] failed to save", "video", document.FileUniqueID, "user", userID, "error", err)
		b.editVideoError(ctx, tgBot, chatID, progressMsg.ID, "⚠️ Не удалось сохранить видео-файл в базе.")
		return
	}
	b.state.SetLastSticker(userID, document.FileUniqueID)

	go func() {
		thumbURL := fileURL
		thumbType := service.StickerTypeVideo
		if document.Thumbnail != nil {
			thumbFile, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: document.Thumbnail.FileID})
			if err == nil {
				thumbURL = tgBot.FileDownloadLink(thumbFile)
				thumbType = service.StickerTypeStatic
			}
		}
		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, document.FileID, thumbURL, thumbType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "video", document.FileUniqueID, "error", err)
		}
	}()

	_, _ = tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      buildOCRResultMessage("видео-файл", text, ocrErr, b.search.Enabled()),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(document.FileUniqueID)},
		},
	})
}
