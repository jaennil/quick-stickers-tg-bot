package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
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
		bot.WithCallbackQueryDataHandler("fallback:", bot.MatchTypePrefix, b.handleFallbackCallback),
		bot.WithCallbackQueryDataHandler("delete:", bot.MatchTypePrefix, b.handleDeleteCallback),
		bot.WithCallbackQueryDataHandler("allstickers:", bot.MatchTypePrefix, b.handleAllStickersCallback),
		bot.WithCallbackQueryDataHandler("pack:", bot.MatchTypePrefix, b.handlePackCallback),
		bot.WithCallbackQueryDataHandler("deletepack:", bot.MatchTypePrefix, b.handleDeletePackCallback),
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
	logger.Log.Info("Bot started")
	b.bot.Start(ctx)
}

// formatIndexReport formats the IndexReport for display to user
func formatIndexReport(title string, report *service.IndexReport) string {
	if report == nil {
		return fmt.Sprintf("✅ Пак \"%s\" добавлен!", title)
	}

	text := fmt.Sprintf("✅ Пак \"%s\" обработан!\n\n📊 Отчёт:\n", title)
	text += fmt.Sprintf("• Всего в паке: %d\n", report.Total)

	if report.SkippedAPI > 0 {
		text += fmt.Sprintf("• Пропущено (уже api): %d\n", report.SkippedAPI)
	}
	if report.SkippedManual > 0 {
		text += fmt.Sprintf("• Пропущено (ручное редактирование): %d\n", report.SkippedManual)
	}
	if report.Reprocessed > 0 {
		text += fmt.Sprintf("• Переобработано: %d\n", report.Reprocessed)
	}
	if report.NewStickers > 0 {
		text += fmt.Sprintf("• Новых стикеров: %d\n", report.NewStickers)
	}
	if report.WithText > 0 {
		text += fmt.Sprintf("• С текстом: %d\n", report.WithText)
	}
	if report.ToProcess == 0 {
		text += "\n💡 Все стикеры уже обработаны!"
	}

	return text
}

// Command handlers

func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	logger.Log.Infow("[CMD] /start", "user", update.Message.From.ID)
	b.sendMainMenu(ctx, tgBot, update.Message.Chat.ID)
}

func (b *Bot) handleHelp(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	logger.Log.Infow("[CMD] /help", "user", update.Message.From.ID)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   helpText,
	})
}

func (b *Bot) handleStats(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	logger.Log.Infow("[CMD] /stats", "user", userID)
	count, err := b.repo.GetUserStickerCount(userID)
	if err != nil {
		logger.Log.Errorw("[CMD] /stats error", "error", err)
		count = 0
	}
	logger.Log.Infow("[CMD] /stats result", "user", userID, "count", count)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("У тебя сохранено стикеров: %d", count),
	})
}

func (b *Bot) handleSettings(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	logger.Log.Infow("[CMD] /settings", "user", userID)
	currentEngine := b.repo.GetUserOCREngine(userID)
	logger.Log.Infow("[CMD] /settings result", "user", userID, "engine", currentEngine)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Текущий OCR движок: %s\n\nВыбери движок для распознавания текста:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboard(currentEngine),
		},
	})
}

func (b *Bot) handleSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	query := strings.TrimPrefix(update.Message.Text, "/search")
	query = strings.TrimSpace(query)
	logger.Log.Infow("[CMD] /search", "user", userID, "query", query)

	if query == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи текст для поиска: /search <текст>",
		})
		return
	}

	b.doSearch(ctx, tgBot, update.Message.Chat.ID, userID, query)
}

func (b *Bot) handleEdit(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	newText := strings.TrimPrefix(update.Message.Text, "/edit")
	newText = strings.TrimSpace(newText)
	logger.Log.Infow("[CMD] /edit", "user", userID, "text", newText)

	if newText == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи текст: /edit <правильный текст>",
		})
		return
	}

	stickerID := b.state.GetLastSticker(userID)
	if stickerID == "" {
		logger.Log.Warnw("[CMD] /edit no last sticker", "user", userID)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Сначала отправь стикер, текст которого хочешь исправить.",
		})
		return
	}

	if err := b.repo.UpdateStickerText(userID, stickerID, newText); err != nil {
		logger.Log.Errorw("[CMD] /edit error", "user", userID, "sticker", stickerID, "error", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при обновлении текста",
		})
		return
	}

	logger.Log.Infow("[CMD] /edit success", "user", userID, "sticker", stickerID, "text", newText)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Текст обновлен на: \"%s\"", newText),
	})
}

func (b *Bot) handleAddPack(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	setName := strings.TrimPrefix(update.Message.Text, "/addpack")
	setName = strings.TrimSpace(setName)
	logger.Log.Infow("[CMD] /addpack", "user", userID, "pack", setName)

	if setName == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Укажи имя стикер-пака: /addpack <имя_пака>\n\nИмя пака можно узнать, переслав мне любой стикер из него.",
		})
		return
	}

	b.doAddPack(ctx, tgBot, update.Message.Chat.ID, userID, setName)
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
	logger.Log.Infow("[CMD] /list", "user", userID, "page", page)

	b.sendStickerListMsg(ctx, tgBot, chatID, userID, page)
}

// Callback handlers

func (b *Bot) handleMenuCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	action := strings.TrimPrefix(update.CallbackQuery.Data, "menu:")
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	userID := update.CallbackQuery.From.ID
	logger.Log.Infow("[CALLBACK] menu", "action", action, "user", userID)

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
		b.sendStickerListMsg(ctx, tgBot, chatID, userID, 1)
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
	logger.Log.Infow("[CALLBACK] ocr", "engine", engine, "user", userID)

	if !constants.IsValidEngine(engine) {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Неизвестный движок",
			ShowAlert:       true,
		})
		return
	}

	if err := b.repo.SetUserOCREngine(userID, engine); err != nil {
		logger.Log.Errorw("Error saving OCR engine", "error", err)
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
		Text:      b.buildSettingsText(engine),
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
	userID := update.CallbackQuery.From.ID
	logger.Log.Infow("[CALLBACK] selectocr", "data", data, "user", userID)
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	stickerID, engine := parts[0], parts[1]

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
		logger.Log.Errorw("Error updating sticker text", "error", err)
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
	logger.Log.Infow("[CALLBACK] edit", "sticker", stickerID, "user", userID)

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

func (b *Bot) handleDeleteCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	stickerID := strings.TrimPrefix(update.CallbackQuery.Data, "delete:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	logger.Log.Infow("[CALLBACK] delete", "sticker", stickerID, "user", userID)

	if err := b.repo.DeleteSticker(userID, stickerID); err != nil {
		logger.Log.Errorw("[CALLBACK] delete error", "sticker", stickerID, "user", userID, "error", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка при удалении",
			ShowAlert:       true,
		})
		return
	}

	logger.Log.Infow("[CALLBACK] delete success", "sticker", stickerID, "user", userID)

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Стикер удалён",
	})

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      "🗑 Стикер удалён",
	})
}

func (b *Bot) handleCancelCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	var targetUserID int64
	fmt.Sscanf(strings.TrimPrefix(update.CallbackQuery.Data, "cancel:"), "%d", &targetUserID)

	callerUserID := update.CallbackQuery.From.ID
	logger.Log.Infow("[CALLBACK] cancel", "target", targetUserID, "user", callerUserID)

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
	logger.Log.Infow("[CALLBACK] addpack", "pack", setName, "user", userID)

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
	userID := update.CallbackQuery.From.ID
	logger.Log.Infow("[CALLBACK] list", "page", pageStr, "user", userID)

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
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	b.sendStickerListMsg(ctx, tgBot, chatID, userID, page)
}

func (b *Bot) handleAllStickersCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	pageStr := strings.TrimPrefix(update.CallbackQuery.Data, "allstickers:")
	userID := update.CallbackQuery.From.ID
	logger.Log.Infow("[CALLBACK] allstickers", "page", pageStr, "user", userID)

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
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	b.sendAllStickers(ctx, tgBot, chatID, userID, page)
}

func (b *Bot) sendAllStickers(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int) {
	offset := (page - 1) * constants.PerPage
	total, _ := b.repo.GetUserStickerCount(userID)

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📋 У тебя пока нет сохранённых стикеров.",
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

		// Build info line with engine/manual edit
		var infoLine string
		if st.ManualEdit {
			infoLine = "✏️ отредактировано"
		} else if st.OCREngine != "" {
			infoLine = fmt.Sprintf("🔍 %s", constants.GetEngineLabel(st.OCREngine))
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		msgText := fmt.Sprintf("Текст: %s", text)
		if infoLine != "" {
			msgText += fmt.Sprintf("\n%s", infoLine)
		}

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   msgText,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(st.StickerID)},
			},
		})
	}

	navButtons := ui.PaginationButtons(page, totalPages, "allstickers")
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📋 Стикеры (страница %d/%d, всего: %d)", page, totalPages, total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
				ui.BackToMenuButton(),
			},
		},
	})
}

func (b *Bot) handlePackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	// Format: pack:setName:page
	data := strings.TrimPrefix(update.CallbackQuery.Data, "pack:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	// Parse setName and page
	lastColon := strings.LastIndex(data, ":")
	if lastColon == -1 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	setName := data[:lastColon]
	pageStr := data[lastColon+1:]

	logger.Log.Infow("[CALLBACK] pack", "pack", setName, "page", pageStr, "user", userID)

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

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	b.sendPackStickers(ctx, tgBot, chatID, userID, setName, page)
}

func (b *Bot) sendPackStickers(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, setName string, page int) {
	offset := (page - 1) * constants.PerPage
	total, _ := b.repo.GetUserPackStickerCount(userID, setName)

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("📦 В паке \"%s\" нет стикеров.", setName),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ К списку паков", CallbackData: "menu:list"}},
				},
			},
		})
		return
	}

	stickers, _ := b.repo.GetUserStickersByPack(userID, setName, constants.PerPage, offset)
	totalPages := (total + constants.PerPage - 1) / constants.PerPage

	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		// Build info line with engine/manual edit
		var infoLine string
		if st.ManualEdit {
			infoLine = "✏️ отредактировано"
		} else if st.OCREngine != "" {
			infoLine = fmt.Sprintf("🔍 %s", constants.GetEngineLabel(st.OCREngine))
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		msgText := fmt.Sprintf("Текст: %s", text)
		if infoLine != "" {
			msgText += fmt.Sprintf("\n%s", infoLine)
		}

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   msgText,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(st.StickerID)},
			},
		})
	}

	navButtons := ui.PaginationButtons(page, totalPages, fmt.Sprintf("pack:%s", setName))
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📦 %s (страница %d/%d, всего: %d)", setName, page, totalPages, total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
				{{Text: "🗑 Удалить пак", CallbackData: "deletepack:" + setName}},
				{{Text: "◀️ К списку паков", CallbackData: "menu:list"}},
			},
		},
	})
}

func (b *Bot) handleDeletePackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	setName := strings.TrimPrefix(update.CallbackQuery.Data, "deletepack:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	logger.Log.Infow("[CALLBACK] deletepack", "pack", setName, "user", userID)

	count, _ := b.repo.GetUserPackStickerCount(userID, setName)

	if err := b.repo.DeleteUserPack(userID, setName); err != nil {
		logger.Log.Errorw("[CALLBACK] deletepack error", "pack", setName, "user", userID, "error", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка при удалении",
			ShowAlert:       true,
		})
		return
	}

	logger.Log.Infow("[CALLBACK] deletepack success", "pack", setName, "user", userID, "deleted", count)

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            fmt.Sprintf("Удалено %d стикеров", count),
	})

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("🗑 Пак \"%s\" удалён (%d стикеров)", setName, count),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "◀️ К списку паков", CallbackData: "menu:list"}},
			},
		},
	})
}

func (b *Bot) handleFallbackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	// Format: fallback:engine:setName
	data := strings.TrimPrefix(update.CallbackQuery.Data, "fallback:")
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	engine, setName := parts[0], parts[1]
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	logger.Log.Infow("[CALLBACK] fallback", "engine", engine, "pack", setName, "user", userID)

	// Get remaining stickers
	storedSetName, remainingStickers, ok := b.state.GetRemainingStickers(userID)
	if !ok || storedSetName != setName {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Данные устарели, начни индексацию заново",
			ShowAlert:       true,
		})
		return
	}

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
		Text:            fmt.Sprintf("Продолжаю с %s...", constants.GetEngineLabel(engine)),
	})

	// Clear remaining stickers
	b.state.ClearRemainingStickers(userID)

	// Continue indexing with selected engine
	go b.continueIndexing(ctx, tgBot, chatID, messageID, userID, setName, engine, remainingStickers)
}

// Default handler

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

// Sticker handler

func (b *Bot) handleSticker(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	sticker := update.Message.Sticker
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	logger.Log.Infow("[STICKER] received", "sticker", sticker.FileUniqueID, "set", sticker.SetName, "user", userID)

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

func (b *Bot) buildSettingsText(currentEngine string) string {
	text := "⚙️ Настройки\n\n"
	text += fmt.Sprintf("Текущий движок: %s\n", constants.GetEngineLabel(currentEngine))
	text += constants.GetEngineDesc(currentEngine) + "\n\n"
	text += "📋 Доступные движки:\n\n"
	for _, e := range constants.OCREngines {
		marker := "○"
		if e.Name == currentEngine {
			marker = "●"
		}
		text += fmt.Sprintf("%s %s\n%s\n\n", marker, e.Label, e.Desc)
	}
	return text
}

func (b *Bot) sendSettingsMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64) {
	currentEngine := b.repo.GetUserOCREngine(userID)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      b.buildSettingsText(currentEngine),
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
		Text:      b.buildSettingsText(currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: ui.OCREngineKeyboardWithBack(currentEngine),
		},
	})
}

func (b *Bot) sendStickerListMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int) {
	total, _ := b.repo.GetUserStickerCount(userID)

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📋 У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или добавь целый пак!",
		})
		return
	}

	// Show pack statistics
	packStats, err := b.repo.GetUserPackStats(userID)
	if err != nil {
		logger.Log.Errorw("[LIST] error getting pack stats", "user", userID, "error", err)
	}

	var msgBuilder strings.Builder
	msgBuilder.WriteString(fmt.Sprintf("📋 Мои стикеры (всего: %d)\n\n", total))

	var buttons [][]models.InlineKeyboardButton

	if len(packStats) > 0 {
		msgBuilder.WriteString("📦 Паки:\n\n")
		for _, ps := range packStats {
			msgBuilder.WriteString(fmt.Sprintf("• %s — %d шт.\n", ps.SetName, ps.Total))
			// Show engine breakdown
			var engines []string
			if ps.ByAPI > 0 {
				engines = append(engines, fmt.Sprintf("☁️ api: %d", ps.ByAPI))
			}
			if ps.ByPaddle > 0 {
				engines = append(engines, fmt.Sprintf("🔷 paddle: %d", ps.ByPaddle))
			}
			if ps.ByEasy > 0 {
				engines = append(engines, fmt.Sprintf("🔶 easy: %d", ps.ByEasy))
			}
			if ps.ByTesseract > 0 {
				engines = append(engines, fmt.Sprintf("📦 tesseract: %d", ps.ByTesseract))
			}
			if ps.ManualEdited > 0 {
				engines = append(engines, fmt.Sprintf("✏️ ручные: %d", ps.ManualEdited))
			}
			if len(engines) > 0 {
				msgBuilder.WriteString("  " + strings.Join(engines, ", ") + "\n")
			}
			msgBuilder.WriteString("\n")

			// Add button for this pack
			buttons = append(buttons, []models.InlineKeyboardButton{
				{Text: fmt.Sprintf("📦 %s (%d)", ps.SetName, ps.Total), CallbackData: fmt.Sprintf("pack:%s:1", ps.SetName)},
			})
		}
	}

	buttons = append(buttons, []models.InlineKeyboardButton{{Text: "📜 Все стикеры", CallbackData: "allstickers:1"}})
	buttons = append(buttons, ui.BackToMenuButton())

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   msgBuilder.String(),
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

func (b *Bot) handleTextSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.Message.Text)
	userID := update.Message.From.ID
	logger.Log.Infow("[TEXT] search", "user", userID, "query", query)

	if len(query) < constants.MinSearchLength {
		logger.Log.Debugw("[TEXT] query too short", "query", query)
		return
	}

	stickers, err := b.repo.SearchByText(userID, query)
	if err != nil {
		logger.Log.Errorw("[TEXT] search error", "user", userID, "query", query, "error", err)
		return
	}
	if len(stickers) == 0 {
		logger.Log.Infow("[TEXT] no results", "user", userID, "query", query)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
		})
		return
	}

	logger.Log.Infow("[TEXT] found", "user", userID, "query", query, "count", len(stickers))
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
	logger.Log.Infow("[SEARCH] searching", "user", userID, "query", query)
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
		logger.Log.Errorw("[SEARCH] error", "user", userID, "query", query, "error", err)
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
		logger.Log.Infow("[SEARCH] no results", "user", userID, "query", query)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.SearchAgainButtons(),
			},
		})
		return
	}

	logger.Log.Infow("[SEARCH] found", "user", userID, "query", query, "count", len(stickers))
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
	logger.Log.Infow("[ADDPACK] starting", "user", userID, "pack", setName)
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
		logger.Log.Warnw("[ADDPACK] pack not found", "pack", setName, "user", userID, "error", err)
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
	logger.Log.Infow("[ADDPACK] found pack", "pack", setName, "title", stickerSet.Title, "stickers", total, "engine", ocrEngine, "user", userID)

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
		logger.Log.Errorw("Error sending progress message", "error", err)
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

	if result.Cancelled && !result.QuotaExceeded {
		logger.Log.Infow("[ADDPACK] cancelled", "pack", setName, "user", userID, "processed", result.Processed)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", stickerSet.Title, result.Processed),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
	} else if result.QuotaExceeded {
		logger.Log.Warnw("[ADDPACK] quota exceeded", "pack", setName, "user", userID, "processed", result.Processed, "remaining", len(result.RemainingStickers))

		// Run comparison on last sticker
		compareText := "⚠️ Квота OCR.space исчерпана!\n\n"
		compareText += fmt.Sprintf("Обработано: %d/%d стикеров\n\n", result.Processed, result.Total)

		if result.LastStickerFileURL != "" {
			compareText += "🔍 Сравнение OCR движков на последнем стикере:\n\n"
			comparison := b.indexer.CompareOCREngines(ctx, result.LastStickerFileURL)
			for _, r := range comparison {
				engineName := constants.GetEngineLabel(r.Engine)
				if r.Error != nil {
					compareText += fmt.Sprintf("❌ %s: ошибка\n", engineName)
				} else if r.Text == "" {
					compareText += fmt.Sprintf("⬜ %s: (пусто)\n", engineName)
				} else {
					compareText += fmt.Sprintf("✅ %s: \"%s\"\n", engineName, r.Text)
				}
			}
			compareText += "\nВыбери движок для продолжения:"
		} else {
			compareText += "Выбери движок для продолжения:"
		}

		// Store remaining stickers for continuation
		b.state.SetRemainingStickers(userID, setName, result.RemainingStickers)

		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      compareText,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.FallbackButtons(setName),
			},
		})
	} else {
		logger.Log.Infow("[ADDPACK] completed", "pack", setName, "user", userID,
			"processed", result.Processed,
			"with_text", result.WithText,
			"skipped_api", result.Report.SkippedAPI,
			"skipped_manual", result.Report.SkippedManual,
			"reprocessed", result.Report.Reprocessed,
		)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      formatIndexReport(stickerSet.Title, result.Report),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.AddPackAgainButtons(),
			},
		})
	}
}

func (b *Bot) continueIndexing(ctx context.Context, tgBot *bot.Bot, chatID int64, messageID int, userID int64, setName string, engine string, stickers []models.Sticker) {
	total := len(stickers)
	logger.Log.Infow("[ADDPACK] continuing with fallback", "pack", setName, "engine", engine, "remaining", total, "user", userID)

	// Create fake sticker set with remaining stickers
	stickerSet := &models.StickerSet{
		Name:     setName,
		Title:    setName,
		Stickers: stickers,
	}

	indexCtx, cancel := context.WithCancel(ctx)
	b.state.SetActiveIndexing(userID, cancel)
	defer b.state.ClearActiveIndexing(userID)

	// Update message
	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("Продолжаю индексацию \"%s\" с %s\n%s 0%%", setName, constants.GetEngineLabel(engine), service.ProgressBar(0, total)),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.CancelButton(userID)},
		},
	})

	result := b.indexer.IndexPack(indexCtx, tgBot, stickerSet, userID, engine, func(p service.IndexProgress) {
		percent := p.Current * 100 / int64(p.Total)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("Продолжаю индексацию \"%s\" с %s\n%s %d%%\n\nОбработано: %d/%d", setName, constants.GetEngineLabel(engine), service.ProgressBar(int(p.Current), p.Total), percent, p.Current, p.Total),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.CancelButton(userID)},
			},
		})
	})

	if result.Cancelled {
		logger.Log.Infow("[ADDPACK] continuation cancelled", "pack", setName, "user", userID, "processed", result.Processed)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", setName, result.Processed),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackToMenuButton()},
			},
		})
	} else {
		logger.Log.Infow("[ADDPACK] continuation completed", "pack", setName, "user", userID, "processed", result.Processed, "with_text", result.WithText)
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      formatIndexReport(setName, result.Report),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.AddPackAgainButtons(),
			},
		})
	}
}

// Inline query handler

func (b *Bot) handleInlineQuery(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.InlineQuery.Query)
	userID := update.InlineQuery.From.ID
	logger.Log.Infow("[INLINE] query", "user", userID, "query", query)

	var results []models.InlineQueryResult

	if len(query) >= constants.MinSearchLength {
		stickers, err := b.repo.SearchByText(userID, query)
		if err != nil {
			logger.Log.Errorw("[INLINE] search error", "user", userID, "query", query, "error", err)
		} else {
			// Limit to 50 results (Telegram max)
			limit := 50
			if len(stickers) < limit {
				limit = len(stickers)
			}

			for i := 0; i < limit; i++ {
				results = append(results, &models.InlineQueryResultCachedSticker{
					ID:            fmt.Sprintf("%d_%s", i, stickers[i].StickerID),
					StickerFileID: stickers[i].FileID,
				})
			}
			logger.Log.Infow("[INLINE] found", "user", userID, "query", query, "total", len(stickers), "returned", limit)
		}
	}

	tgBot.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: update.InlineQuery.ID,
		Results:       results,
		CacheTime:     300, // 5 minutes cache
		IsPersonal:    true,
	})
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
