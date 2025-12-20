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
	"github.com/jaennil/sticker-search-bot/internal/state"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) defaultHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	// Handle inline queries
	if update.InlineQuery != nil {
		b.handleInlineQuery(ctx, tgBot, update)
		return
	}

	if update.Message == nil {
		return
	}

	if update.Message.Sticker != nil {
		b.handleSticker(ctx, tgBot, update)
		return
	}

	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	// Menu button handling
	switch text {
	case "🔍 Поиск":
		b.state.SetAwaitingMode(userID, state.ModeSearch)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "🔍 Введи текст для поиска:"})
		return
	case "📦 Добавить пак":
		b.state.SetAwaitingMode(userID, state.ModeAddPack)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "📦 Введи имя стикер-пака:\n\nИмя пака можно узнать, отправив мне любой стикер из него."})
		return
	case "📋 Мои стикеры":
		b.sendStickerListMsg(ctx, tgBot, chatID, userID, 1)
		return
	case "⚙️ Настройки":
		b.sendSettingsMsg(ctx, tgBot, chatID, userID)
		return
	case "📊 Статистика":
		count, _ := b.repo.GetUserStickerCount(userID)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("📊 Статистика\n\nСохранено стикеров: %d", count)})
		return
	case "❓ Помощь":
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "❓ Помощь\n\n" + helpTextShort})
		return
	}

	// Awaiting mode handling
	if text != "" && !strings.HasPrefix(text, "/") {
		switch b.state.GetAwaitingMode(userID) {
		case state.ModeEdit:
			b.handleAwaitingEdit(ctx, tgBot, update)
			return
		case state.ModeSearch:
			b.state.ClearAwaitingMode(userID)
			b.doSearch(ctx, tgBot, chatID, userID, text)
			return
		case state.ModeAddPack:
			b.state.ClearAwaitingMode(userID)
			b.doAddPack(ctx, tgBot, chatID, userID, strings.TrimSpace(text))
			return
		}

		// Text search
		b.handleTextSearch(ctx, tgBot, update)
	}
}

func (b *Bot) handleSticker(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	sticker := update.Message.Sticker
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	logger.Log.Infow("[STICKER] received",
		"sticker", sticker.FileUniqueID,
		"set", sticker.SetName,
		"user", userID,
		"is_animated", sticker.IsAnimated,
		"is_video", sticker.IsVideo,
	)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Распознаю текст...",
	})
	if err != nil {
		logger.Log.Errorw("Error sending progress message", "error", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
	if err != nil {
		logger.Log.Errorw("Error getting file", "error", err)
		return
	}

	fileURL := tgBot.FileDownloadLink(file)

	// Determine sticker type
	var stickerType service.StickerType
	if sticker.IsVideo {
		stickerType = service.StickerTypeVideo
	} else if sticker.IsAnimated {
		stickerType = service.StickerTypeAnimated
	} else {
		stickerType = service.StickerTypeStatic
	}

	// Run all OCR engines in parallel
	results := make(map[string]string)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	for _, engine := range constants.OCREngines {
		wg.Add(1)
		go func(engineName string) {
			defer wg.Done()
			text, err := b.indexer.DownloadAndOCRWithType(ctx, fileURL, engineName, stickerType)
			if err != nil {
				logger.Log.Errorw("OCR error", "engine", engineName, "error", err)
				text = ""
			}
			resultsMu.Lock()
			results[engineName] = text
			resultsMu.Unlock()
		}(engine.Name)
	}
	wg.Wait()

	// Log results
	for name, text := range results {
		if text != "" {
			logger.Log.Infow("[OCR] result", "sticker", sticker.FileUniqueID, "engine", name, "text", text)
		}
	}

	// Save results for selection
	b.state.SetPendingOCR(sticker.FileUniqueID, results)
	b.state.SetLastSticker(userID, sticker.FileUniqueID)

	// Decode document_id from file_id
	documentID, _ := fileid.DecodeDocumentID(sticker.FileID)

	// Save sticker without text
	s := &repository.Sticker{
		UserID:     userID,
		StickerID:  sticker.FileUniqueID,
		SetName:    sticker.SetName,
		FileID:     sticker.FileID,
		DocumentID: documentID,
		Text:       "",
		Emoji:      sticker.Emoji,
		IsAnimated: sticker.IsAnimated,
		IsVideo:    sticker.IsVideo,
	}
	b.repo.SaveSticker(s)

	// Save thumbnail in background
	go func() {
		thumbURL := fileURL
		thumbType := stickerType

		// For animated/video stickers, use Telegram's built-in thumbnail
		if (sticker.IsAnimated || sticker.IsVideo) && sticker.Thumbnail != nil {
			thumbFile, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.Thumbnail.FileID})
			if err == nil {
				thumbURL = tgBot.FileDownloadLink(thumbFile)
				thumbType = service.StickerTypeStatic // Telegram thumbnail is already a static image
			}
		}

		if err := b.indexer.DownloadAndSaveThumbnailWithType(ctx, sticker.FileID, thumbURL, thumbType); err != nil {
			logger.Log.Debugw("[THUMB] failed to save", "sticker", sticker.FileUniqueID, "error", err)
		}
	}()

	// Build results message
	var msgBuilder strings.Builder
	msgBuilder.WriteString("Результаты распознавания:\n\n")

	var buttons [][]models.InlineKeyboardButton
	hasResults := false

	for _, engine := range constants.OCREngines {
		text := results[engine.Name]
		if text != "" {
			hasResults = true
			msgBuilder.WriteString(fmt.Sprintf("%s:\n%s\n\n", engine.Label, text))
			buttons = append(buttons, []models.InlineKeyboardButton{
				{Text: fmt.Sprintf("✓ %s", engine.Label), CallbackData: fmt.Sprintf("selectocr:%s:%s", sticker.FileUniqueID, engine.Name)},
			})
		} else {
			msgBuilder.WriteString(fmt.Sprintf("%s:\n(не распознано)\n\n", engine.Label))
		}
	}

	if !hasResults {
		msgBuilder.WriteString("Текст не распознан ни одним движком.")
	}

	buttons = append(buttons, ui.EditStickerButton(sticker.FileUniqueID))
	if sticker.SetName != "" {
		buttons = append(buttons, ui.AddPackButton(sticker.SetName))
	}

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      msgBuilder.String(),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}

func (b *Bot) handleAwaitingEdit(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	newText := strings.TrimSpace(update.Message.Text)
	logger.Log.Infow("[EDIT] awaiting", "user", userID, "text", newText)

	b.state.ClearAwaitingMode(userID)

	stickerID := b.state.GetLastSticker(userID)
	if stickerID == "" {
		logger.Log.Warnw("[EDIT] no last sticker", "user", userID)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка: стикер не найден",
		})
		return
	}

	if err := b.repo.UpdateStickerText(userID, stickerID, newText); err != nil {
		logger.Log.Errorw("[EDIT] error updating", "user", userID, "sticker", stickerID, "error", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при обновлении текста",
		})
		return
	}

	logger.Log.Infow("[EDIT] success", "user", userID, "sticker", stickerID, "text", newText)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("✅ Текст обновлен: \"%s\"", newText),
	})
}
