package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	results := make(map[string]string)
	ocrErrors := make(map[string]error)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	for _, engine := range constants.OCREngines {
		wg.Add(1)
		go func(engineName string) {
			defer wg.Done()
			text, err := b.indexer.DownloadAndOCRWithType(ctx, fileURL, engineName, photoType)
			if err != nil {
				logger.Log.Errorw("OCR error", "engine", engineName, "error", err)
			}
			resultsMu.Lock()
			results[engineName] = text
			ocrErrors[engineName] = err
			resultsMu.Unlock()
		}(engine.Name)
	}
	wg.Wait()

	for name, text := range results {
		if text != "" {
			logger.Log.Infow("[OCR] result", "photo", photo.FileUniqueID, "engine", name, "text", text)
		}
	}

	b.state.SetPendingOCR(photo.FileUniqueID, results)
	b.state.SetLastSticker(userID, photo.FileUniqueID)

	p := &repository.Sticker{
		UserID:    userID,
		StickerID: photo.FileUniqueID,
		SetName:   "",
		FileID:    photo.FileID,
		Text:      "",
		Emoji:     "",
		MediaType: repository.MediaTypePhoto,
	}
	b.repo.SaveSticker(p)

	go func() {
		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, photo.FileID, fileURL, photoType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "photo", photo.FileUniqueID, "error", err)
		}
	}()

	var msgBuilder strings.Builder
	msgBuilder.WriteString("Результаты распознавания картинки:\n\n")

	var buttons [][]models.InlineKeyboardButton
	hasResults := false

	for _, engine := range constants.OCREngines {
		text := results[engine.Name]
		if text != "" {
			hasResults = true
			msgBuilder.WriteString(fmt.Sprintf("%s:\n%s\n\n", engine.Label, text))
			buttons = append(buttons, []models.InlineKeyboardButton{
				{Text: fmt.Sprintf("✓ %s", engine.Label), CallbackData: fmt.Sprintf("selectocr:%s:%s", photo.FileUniqueID, engine.Name)},
			})
		} else if err := ocrErrors[engine.Name]; err != nil {
			msgBuilder.WriteString(fmt.Sprintf("%s:\n⚠️ Ошибка: %s\n\n", engine.Label, err.Error()))
		} else {
			msgBuilder.WriteString(fmt.Sprintf("%s:\n(текст не найден)\n\n", engine.Label))
		}
	}

	if !hasResults {
		msgBuilder.WriteString("Текст не распознан ни одним движком.")
	}

	buttons = append(buttons, ui.EditStickerButton(photo.FileUniqueID))

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      msgBuilder.String(),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}
