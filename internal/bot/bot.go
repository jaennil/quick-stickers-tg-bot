package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/state"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

type Bot struct {
	bot     *bot.Bot
	repo    repository.Repository
	ocr     *ocr.OCR
	indexer *service.Indexer
	state   *state.Manager
}

func New(token string, repo repository.Repository, ocr *ocr.OCR) (*Bot, error) {
	b := &Bot{
		repo:    repo,
		ocr:     ocr,
		indexer: service.NewIndexer(repo, ocr),
		state:   state.NewManager(30 * time.Minute),
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.defaultHandler),
		bot.WithCallbackQueryDataHandler("menu:", bot.MatchTypePrefix, b.handleMenuCallback),
		bot.WithCallbackQueryDataHandler("addpack:", bot.MatchTypePrefix, b.handleAddPackCallback),
		bot.WithCallbackQueryDataHandler("edit:", bot.MatchTypePrefix, b.handleEditCallback),
		bot.WithCallbackQueryDataHandler("cancel:", bot.MatchTypePrefix, b.handleCancelCallback),
		bot.WithCallbackQueryDataHandler("ocr:", bot.MatchTypePrefix, b.handleOCRCallback),
		bot.WithCallbackQueryDataHandler("selectocr:", bot.MatchTypePrefix, b.handleSelectOCRCallback),
		bot.WithCallbackQueryDataHandler("list:", bot.MatchTypePrefix, b.handleListCallback),
	}

	tgBot, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	b.bot = tgBot
	b.registerHandlers()

	return b, nil
}

func (b *Bot) registerHandlers() {
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, b.handleStart)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, b.handleHelp)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, b.handleStats)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/search", bot.MatchTypePrefix, b.handleSearch)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/addpack", bot.MatchTypePrefix, b.handleAddPack)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/edit", bot.MatchTypePrefix, b.handleEdit)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/settings", bot.MatchTypeExact, b.handleSettings)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/list", bot.MatchTypePrefix, b.handleList)
}

func (b *Bot) Start(ctx context.Context) {
	log.Println("Bot started")
	b.bot.Start(ctx)
}

// Command handlers

func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	b.sendMainMenu(ctx, tgBot, update.Message.Chat.ID)
}

func (b *Bot) handleHelp(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   helpText,
	})
}

func (b *Bot) handleStats(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	count, err := b.repo.GetUserStickerCount(update.Message.From.ID)
	if err != nil {
		log.Printf("Error getting stats: %v", err)
		count = 0
	}
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("У тебя сохранено стикеров: %d", count),
	})
}

func (b *Bot) handleSettings(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	currentEngine := b.repo.GetUserOCREngine(userID)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Текущий OCR движок: %s\n\nВыбери движок для распознавания текста:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboard(currentEngine),
		},
	})
}

func (b *Bot) handleSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimPrefix(update.Message.Text, "/search")
	query = strings.TrimSpace(query)

	if query == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи текст для поиска: /search <текст>",
		})
		return
	}

	b.doSearch(ctx, tgBot, update.Message.Chat.ID, update.Message.From.ID, query)
}

func (b *Bot) handleEdit(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	newText := strings.TrimPrefix(update.Message.Text, "/edit")
	newText = strings.TrimSpace(newText)

	if newText == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи текст: /edit <правильный текст>",
		})
		return
	}

	userID := update.Message.From.ID
	stickerID := b.state.GetLastSticker(userID)
	if stickerID == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Сначала отправь стикер, текст которого хочешь исправить.",
		})
		return
	}

	if err := b.repo.UpdateStickerText(userID, stickerID, newText); err != nil {
		log.Printf("Error updating sticker text: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при обновлении текста",
		})
		return
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Текст обновлен на: \"%s\"", newText),
	})
}

func (b *Bot) handleAddPack(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	setName := strings.TrimPrefix(update.Message.Text, "/addpack")
	setName = strings.TrimSpace(setName)

	if setName == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи имя стикер-пака: /addpack <имя_пака>\n\nИмя пака можно узнать, переслав мне любой стикер из него.",
		})
		return
	}

	b.doAddPack(ctx, tgBot, update.Message.Chat.ID, update.Message.From.ID, setName)
}

func (b *Bot) handleList(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	pageStr := strings.TrimPrefix(update.Message.Text, "/list")
	pageStr = strings.TrimSpace(pageStr)
	page := 1
	if pageStr != "" {
		fmt.Sscanf(pageStr, "%d", &page)
		if page < 1 {
			page = 1
		}
	}

	b.sendStickerListMsg(ctx, tgBot, chatID, userID, page)
}

// Callback handlers

func (b *Bot) handleMenuCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	action := strings.TrimPrefix(update.CallbackQuery.Data, "menu:")
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	userID := update.CallbackQuery.From.ID

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	switch action {
	case "main":
		b.sendMainMenu(ctx, tgBot, chatID)
	case "search":
		b.state.SetAwaitingMode(userID, state.ModeSearch)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "🔍 Введи текст для поиска:",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackButton()},
			},
		})
	case "addpack":
		b.state.SetAwaitingMode(userID, state.ModeAddPack)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "📦 Введи имя стикер-пака:\n\nИмя пака можно узнать, отправив мне любой стикер из него.",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackButton()},
			},
		})
	case "list":
		b.sendStickerList(ctx, tgBot, chatID, userID, 1, messageID)
	case "settings":
		b.sendSettings(ctx, tgBot, chatID, userID, messageID)
	case "stats":
		count, _ := b.repo.GetUserStickerCount(userID)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("📊 Статистика\n\nСохранено стикеров: %d", count),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackButton()},
			},
		})
	case "help":
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "❓ Помощь\n\n" + helpTextShort,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackButton()},
			},
		})
	}
}

func (b *Bot) handleOCRCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	engine := strings.TrimPrefix(update.CallbackQuery.Data, "ocr:")
	userID := update.CallbackQuery.From.ID

	if !constants.IsValidEngine(engine) {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Неизвестный движок",
			ShowAlert:       true,
		})
		return
	}

	if err := b.repo.SetUserOCREngine(userID, engine); err != nil {
		log.Printf("Error saving OCR engine: %v", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка сохранения",
			ShowAlert:       true,
		})
		return
	}

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      fmt.Sprintf("Текущий OCR движок: %s\n\nВыбери движок для распознавания текста:", engine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboard(engine),
		},
	})

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            fmt.Sprintf("Выбран: %s", constants.GetEngineLabel(engine)),
	})
}

func (b *Bot) handleSelectOCRCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	data := strings.TrimPrefix(update.CallbackQuery.Data, "selectocr:")
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	stickerID, engine := parts[0], parts[1]
	userID := update.CallbackQuery.From.ID

	results, ok := b.state.GetPendingOCR(stickerID)
	if !ok {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Результаты устарели, отправь стикер заново",
			ShowAlert:       true,
		})
		return
	}

	text := results[engine]
	if err := b.repo.UpdateStickerText(userID, stickerID, text); err != nil {
		log.Printf("Error updating sticker text: %v", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка сохранения",
			ShowAlert:       true,
		})
		return
	}

	b.state.DeletePendingOCR(stickerID)

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      fmt.Sprintf("✅ Сохранено!\n\nДвижок: %s\nТекст: \"%s\"", constants.GetEngineLabel(engine), text),
	})

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Сохранено!",
	})
}

func (b *Bot) handleEditCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	stickerID := strings.TrimPrefix(update.CallbackQuery.Data, "edit:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	b.state.SetLastSticker(userID, stickerID)
	b.state.SetAwaitingMode(userID, state.ModeEdit)

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Введи правильный текст для этого стикера:",
	})
}

func (b *Bot) handleCancelCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	var targetUserID int64
	fmt.Sscanf(strings.TrimPrefix(update.CallbackQuery.Data, "cancel:"), "%d", &targetUserID)

	callerUserID := update.CallbackQuery.From.ID
	if callerUserID != targetUserID {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ты не можешь отменить чужую индексацию",
			ShowAlert:       true,
		})
		return
	}

	cancel, exists := b.state.GetActiveIndexing(targetUserID)
	if !exists {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Индексация уже завершена",
		})
		return
	}

	cancel()
	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Отменяю...",
	})
}

func (b *Bot) handleAddPackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	setName := strings.TrimPrefix(update.CallbackQuery.Data, "addpack:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	if b.state.HasActiveIndexing(userID) {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "У тебя уже идёт индексация",
			ShowAlert:       true,
		})
		return
	}

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Начинаю индексацию...",
	})

	b.doAddPack(ctx, tgBot, chatID, userID, setName)
}

func (b *Bot) handleListCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	pageStr := strings.TrimPrefix(update.CallbackQuery.Data, "list:")
	if pageStr == "noop" {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
		})
		return
	}

	var page int
	fmt.Sscanf(pageStr, "%d", &page)
	if page < 1 {
		page = 1
	}

	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	b.sendStickerListMsg(ctx, tgBot, chatID, userID, page)
}

// Default handler

func (b *Bot) defaultHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
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

// Sticker handler

func (b *Bot) handleSticker(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	sticker := update.Message.Sticker
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Распознаю текст...",
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
	if err != nil {
		log.Printf("Error getting file: %v", err)
		return
	}

	fileURL := tgBot.FileDownloadLink(file)

	// Run all OCR engines in parallel
	results := make(map[string]string)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	for _, engine := range constants.OCREngines {
		wg.Add(1)
		go func(engineName string) {
			defer wg.Done()
			text, err := b.indexer.DownloadAndOCR(ctx, fileURL, engineName)
			if err != nil {
				log.Printf("OCR error (%s): %v", engineName, err)
				text = ""
			}
			resultsMu.Lock()
			results[engineName] = text
			resultsMu.Unlock()
		}(engine.Name)
	}
	wg.Wait()

	// Log results
	log.Printf("OCR results for sticker %s:", sticker.FileUniqueID)
	for name, text := range results {
		if text != "" {
			log.Printf("  %s: %q", name, text)
		}
	}

	// Save results for selection
	b.state.SetPendingOCR(sticker.FileUniqueID, results)
	b.state.SetLastSticker(userID, sticker.FileUniqueID)

	// Save sticker without text
	s := &repository.Sticker{
		UserID:    userID,
		StickerID: sticker.FileUniqueID,
		SetName:   sticker.SetName,
		FileID:    sticker.FileID,
		Text:      "",
		Emoji:     sticker.Emoji,
	}
	b.repo.SaveSticker(s)

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

// Helper methods

func (b *Bot) sendMainMenu(ctx context.Context, tgBot *bot.Bot, chatID int64) {
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        mainMenuText,
		ReplyMarkup: ui.MainMenuKeyboard(),
	})
}

func (b *Bot) sendSettingsMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64) {
	currentEngine := b.repo.GetUserOCREngine(userID)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("⚙️ Настройки\n\nТекущий OCR движок: %s\n\nВыбери движок для распознавания:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboard(currentEngine),
		},
	})
}

func (b *Bot) sendSettings(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, messageID int) {
	currentEngine := b.repo.GetUserOCREngine(userID)
	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("⚙️ Настройки\n\nТекущий OCR движок: %s\n\nВыбери движок для распознавания:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboardWithBack(currentEngine),
		},
	})
}

func (b *Bot) sendStickerListMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int) {
	offset := (page - 1) * constants.PerPage
	total, _ := b.repo.GetUserStickerCount(userID)

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📋 У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или добавь целый пак!",
		})
		return
	}

	stickers, _ := b.repo.GetUserStickers(userID, constants.PerPage, offset)
	totalPages := (total + constants.PerPage - 1) / constants.PerPage

	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Текст: %s", text),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(st.StickerID)},
			},
		})
	}

	navButtons := ui.PaginationButtons(page, totalPages, "list")
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📋 Стикеры (всего: %d)", total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{navButtons},
		},
	})
}

func (b *Bot) sendStickerList(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int, messageID int) {
	offset := (page - 1) * constants.PerPage
	total, _ := b.repo.GetUserStickerCount(userID)

	if total == 0 {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "📋 У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или добавь целый пак!",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.EmptyListButtons(),
			},
		})
		return
	}

	stickers, _ := b.repo.GetUserStickers(userID, constants.PerPage, offset)
	totalPages := (total + constants.PerPage - 1) / constants.PerPage

	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Текст: %s", text),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(st.StickerID)},
			},
		})
	}

	navButtons := ui.PaginationButtons(page, totalPages, "list")
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📋 Стикеры (всего: %d)", total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
				ui.BackToMenuButton(),
			},
		},
	})
}

func (b *Bot) handleAwaitingEdit(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	newText := strings.TrimSpace(update.Message.Text)

	b.state.ClearAwaitingMode(userID)

	stickerID := b.state.GetLastSticker(userID)
	if stickerID == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка: стикер не найден",
		})
		return
	}

	if err := b.repo.UpdateStickerText(userID, stickerID, newText); err != nil {
		log.Printf("Error updating sticker text: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при обновлении текста",
		})
		return
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("✅ Текст обновлен: \"%s\"", newText),
	})
}

func (b *Bot) handleTextSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.Message.Text)
	if len(query) < constants.MinSearchLength {
		return
	}

	stickers, err := b.repo.SearchByText(update.Message.From.ID, query)
	if err != nil || len(stickers) == 0 {
		if len(stickers) == 0 {
			tgBot.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: update.Message.Chat.ID,
				Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
			})
		}
		return
	}

	limit := 5
	if len(stickers) < limit {
		limit = len(stickers)
	}

	for i := 0; i < limit; i++ {
		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  update.Message.Chat.ID,
			Sticker: &models.InputFileString{Data: stickers[i].FileID},
		})
	}
}

func (b *Bot) doSearch(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, query string) {
	query = strings.TrimSpace(query)
	if len(query) < constants.MinSearchLength {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Слишком короткий запрос. Введи минимум 2 символа.",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
		return
	}

	stickers, err := b.repo.SearchByText(userID, query)
	if err != nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Ошибка при поиске",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
		return
	}

	if len(stickers) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.SearchAgainButtons(),
			},
		})
		return
	}

	limit := constants.SearchResultLimit
	if len(stickers) < limit {
		limit = len(stickers)
	}

	for i := 0; i < limit; i++ {
		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: stickers[i].FileID},
		})
	}

	msg := fmt.Sprintf("Найдено: %d", len(stickers))
	if len(stickers) > constants.SearchResultLimit {
		msg = fmt.Sprintf("Показано %d из %d найденных", constants.SearchResultLimit, len(stickers))
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   msg,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.SearchAgainButtons(),
		},
	})
}

func (b *Bot) doAddPack(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, setName string) {
	if setName == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Имя пака не может быть пустым",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
		return
	}

	if b.state.HasActiveIndexing(userID) {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "У тебя уже идёт индексация. Дождись завершения или отмени её.",
		})
		return
	}

	stickerSet, err := tgBot.GetStickerSet(ctx, &bot.GetStickerSetParams{Name: setName})
	if err != nil {
		log.Printf("Error getting sticker set: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось найти стикер-пак '%s'. Проверь правильность имени.", setName),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.TryAgainAddPackButtons(),
			},
		})
		return
	}

	total := len(stickerSet.Stickers)
	ocrEngine := b.repo.GetUserOCREngine(userID)

	indexCtx, cancel := context.WithCancel(ctx)
	b.state.SetActiveIndexing(userID, cancel)
	defer b.state.ClearActiveIndexing(userID)

	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Индексирую пак \"%s\"\n%s 0%%", stickerSet.Title, service.ProgressBar(0, total)),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.CancelButton(userID)},
		},
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	result := b.indexer.IndexPack(indexCtx, tgBot, stickerSet, userID, ocrEngine, func(p service.IndexProgress) {
		percent := p.Current * 100 / int64(p.Total)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("Индексирую пак \"%s\"\n%s %d%%\n\nОбработано: %d/%d", stickerSet.Title, service.ProgressBar(int(p.Current), p.Total), percent, p.Current, p.Total),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.CancelButton(userID)},
			},
		})
	})

	if result.Cancelled {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", stickerSet.Title, result.Processed),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
	} else {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("✅ Пак \"%s\" добавлен!\n\nСтикеров: %d\nС текстом: %d", stickerSet.Title, result.Processed, result.WithText),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.AddPackAgainButtons(),
			},
		})
	}
}

// Text constants

const mainMenuText = `Привет! Я помогу найти нужный стикер по тексту.

Отправь мне стикер — я распознаю текст и сохраню его.
Потом сможешь искать стикеры по тексту!`

const helpText = `Как добавить стикеры:
• Перешли мне стикер — добавлю один
• /addpack <имя_пака> — добавлю весь пак

Имя пака можно узнать: перешли стикер → в ответе будет имя пака.

Как искать:
/search пятница — найдет стикеры с текстом "пятница"
Или просто напиши текст — тоже найду!

Исправить текст:
/edit <правильный текст> — после отправки стикера

Настройки:
/settings — выбрать движок OCR (PaddleOCR, EasyOCR, OCR.space API, Tesseract)`

const helpTextShort = `Как добавить стикеры:
• Отправь мне стикер — добавлю один
• Нажми "📦 Добавить пак" — добавлю весь пак

Как искать:
• Нажми "🔍 Поиск" и введи текст
• Или просто напиши текст — тоже найду!

Как исправить текст:
• В списке стикеров нажми "✏️ Изменить текст"`
