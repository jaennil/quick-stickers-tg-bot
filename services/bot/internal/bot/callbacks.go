package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/state"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) handleMenuCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	action := strings.TrimPrefix(update.CallbackQuery.Data, CallbackMenu)
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
	engine := strings.TrimPrefix(update.CallbackQuery.Data, CallbackOCR)
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
	data := strings.TrimPrefix(update.CallbackQuery.Data, CallbackSelectOCR)
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
	stickerID := strings.TrimPrefix(update.CallbackQuery.Data, CallbackEdit)
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
	stickerID := strings.TrimPrefix(update.CallbackQuery.Data, CallbackDelete)
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
	fmt.Sscanf(strings.TrimPrefix(update.CallbackQuery.Data, CallbackCancel), "%d", &targetUserID)

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
	setName := strings.TrimPrefix(update.CallbackQuery.Data, CallbackAddPack)
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
	pageStr := strings.TrimPrefix(update.CallbackQuery.Data, CallbackList)
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
	pageStr := strings.TrimPrefix(update.CallbackQuery.Data, CallbackAllStickers)
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

func (b *Bot) handlePackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	data := strings.TrimPrefix(update.CallbackQuery.Data, CallbackPack)
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

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

func (b *Bot) handleDeletePackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	setName := strings.TrimPrefix(update.CallbackQuery.Data, CallbackDeletePack)
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
				{{Text: "◀️ К списку паков", CallbackData: CallbackMenu + "list"}},
			},
		},
	})
}

func (b *Bot) handleFallbackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	data := strings.TrimPrefix(update.CallbackQuery.Data, CallbackFallback)
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

	b.state.ClearRemainingStickers(userID)

	go b.continueIndexing(ctx, tgBot, chatID, messageID, userID, setName, engine, remainingStickers)
}

func (b *Bot) handleMediaCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	data := strings.TrimPrefix(update.CallbackQuery.Data, CallbackMedia)
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	mediaTypeStr, pageStr := parts[0], parts[1]
	logger.Log.Infow("[CALLBACK] media", "type", mediaTypeStr, "page", pageStr, "user", userID)

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

	mediaType := repository.MediaTypeSticker
	if mediaTypeStr == "photo" {
		mediaType = repository.MediaTypePhoto
	}

	b.sendMediaByType(ctx, tgBot, chatID, userID, mediaType, page)
}
