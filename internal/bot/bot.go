package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

type Bot struct {
	bot              *bot.Bot
	repo             repository.Repository
	ocr              *ocr.OCR
	lastSticker      map[int64]string // userID -> stickerID
	lastStickerMu    sync.RWMutex
	awaitingEdit     map[int64]bool // userID -> waiting for edit text
	awaitingEditMu   sync.RWMutex
	awaitingSearch   map[int64]bool // userID -> waiting for search text
	awaitingSearchMu sync.RWMutex
	awaitingAddpack   map[int64]bool // userID -> waiting for pack name
	awaitingAddpackMu sync.RWMutex
	activeIndexing   map[int64]context.CancelFunc // userID -> cancel function
	activeIndexingMu sync.RWMutex
	pendingOCR       map[string]map[string]string // stickerID -> engine -> text
	pendingOCRMu     sync.RWMutex
}

func New(token string, repo repository.Repository, ocr *ocr.OCR) (*Bot, error) {
	b := &Bot{
		repo:            repo,
		ocr:             ocr,
		lastSticker:     make(map[int64]string),
		awaitingEdit:    make(map[int64]bool),
		awaitingSearch:  make(map[int64]bool),
		awaitingAddpack: make(map[int64]bool),
		activeIndexing:  make(map[int64]context.CancelFunc),
		pendingOCR:      make(map[string]map[string]string),
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

func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	b.sendMainMenu(ctx, tgBot, update.Message.Chat.ID)
}

func (b *Bot) sendMainMenu(ctx context.Context, tgBot *bot.Bot, chatID int64) {
	msg := `Привет! Я помогу найти нужный стикер по тексту.

Отправь мне стикер — я распознаю текст и сохраню его.
Потом сможешь искать стикеры по тексту!`

	keyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{{Text: "🔍 Поиск"}, {Text: "📦 Добавить пак"}},
			{{Text: "📋 Мои стикеры"}, {Text: "⚙️ Настройки"}},
			{{Text: "📊 Статистика"}, {Text: "❓ Помощь"}},
		},
		ResizeKeyboard: true,
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        msg,
		ReplyMarkup: keyboard,
	})
}

func (b *Bot) sendSettingsMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64) {
	currentEngine := b.repo.GetUserOCREngine(userID)

	engines := []struct {
		name  string
		label string
	}{
		{"api", "OCR.space"},
		{"paddle", "PaddleOCR"},
		{"easy", "EasyOCR"},
		{"tesseract", "Tesseract"},
	}

	var buttons [][]models.InlineKeyboardButton
	for _, e := range engines {
		label := e.label
		if e.name == currentEngine {
			label = "✓ " + label
		}
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: label, CallbackData: "ocr:" + e.name},
		})
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("⚙️ Настройки\n\nТекущий OCR движок: %s\n\nВыбери движок для распознавания:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}

func (b *Bot) sendStickerListMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int) {
	const perPage = 5
	offset := (page - 1) * perPage

	total, _ := b.repo.GetUserStickerCount(userID)
	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📋 У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или добавь целый пак!",
		})
		return
	}

	stickers, _ := b.repo.GetUserStickers(userID, perPage, offset)
	totalPages := (total + perPage - 1) / perPage

	// Отправляем стикеры
	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		info := fmt.Sprintf("Текст: %s", text)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   info,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "✏️ Изменить текст", CallbackData: "edit:" + st.StickerID}},
				},
			},
		})
	}

	// Навигация
	var navButtons []models.InlineKeyboardButton
	if page > 1 {
		navButtons = append(navButtons, models.InlineKeyboardButton{Text: "◀️", CallbackData: fmt.Sprintf("list:%d", page-1)})
	}
	navButtons = append(navButtons, models.InlineKeyboardButton{Text: fmt.Sprintf("%d/%d", page, totalPages), CallbackData: "list:noop"})
	if page < totalPages {
		navButtons = append(navButtons, models.InlineKeyboardButton{Text: "▶️", CallbackData: fmt.Sprintf("list:%d", page+1)})
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📋 Стикеры (всего: %d)", total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
			},
		},
	})
}

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
		b.awaitingSearchMu.Lock()
		b.awaitingSearch[userID] = true
		b.awaitingSearchMu.Unlock()

		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "🔍 Введи текст для поиска:",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ Назад", CallbackData: "menu:main"}},
				},
			},
		})

	case "addpack":
		b.awaitingAddpackMu.Lock()
		b.awaitingAddpack[userID] = true
		b.awaitingAddpackMu.Unlock()

		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "📦 Введи имя стикер-пака:\n\nИмя пака можно узнать, отправив мне любой стикер из него.",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ Назад", CallbackData: "menu:main"}},
				},
			},
		})

	case "list":
		b.sendStickerList(ctx, tgBot, chatID, userID, 1, messageID)

	case "settings":
		b.sendSettings(ctx, tgBot, chatID, userID, messageID)

	case "stats":
		count, err := b.repo.GetUserStickerCount(userID)
		if err != nil {
			count = 0
		}
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("📊 Статистика\n\nСохранено стикеров: %d", count),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ Назад", CallbackData: "menu:main"}},
				},
			},
		})

	case "help":
		msg := `❓ Помощь

Как добавить стикеры:
• Отправь мне стикер — добавлю один
• Нажми "📦 Добавить пак" — добавлю весь пак

Как искать:
• Нажми "🔍 Поиск" и введи текст
• Или просто напиши текст — тоже найду!

Как исправить текст:
• В списке стикеров нажми "✏️ Изменить текст"`

		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      msg,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ Назад", CallbackData: "menu:main"}},
				},
			},
		})
	}
}

func (b *Bot) sendSettings(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, messageID int) {
	currentEngine := b.repo.GetUserOCREngine(userID)

	engines := []struct {
		name  string
		label string
	}{
		{"api", "OCR.space"},
		{"paddle", "PaddleOCR"},
		{"easy", "EasyOCR"},
		{"tesseract", "Tesseract"},
	}

	var buttons [][]models.InlineKeyboardButton
	for _, e := range engines {
		label := e.label
		if e.name == currentEngine {
			label = "✓ " + label
		}
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: label, CallbackData: "ocr:" + e.name},
		})
	}
	buttons = append(buttons, []models.InlineKeyboardButton{
		{Text: "◀️ Назад", CallbackData: "menu:main"},
	})

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("⚙️ Настройки\n\nТекущий OCR движок: %s\n\nВыбери движок для распознавания:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}

func (b *Bot) sendStickerList(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int, messageID int) {
	const perPage = 5
	offset := (page - 1) * perPage

	total, _ := b.repo.GetUserStickerCount(userID)
	if total == 0 {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      "📋 У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или добавь целый пак!",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "📦 Добавить пак", CallbackData: "menu:addpack"}},
					{{Text: "◀️ Назад", CallbackData: "menu:main"}},
				},
			},
		})
		return
	}

	stickers, _ := b.repo.GetUserStickers(userID, perPage, offset)
	totalPages := (total + perPage - 1) / perPage

	// Отправляем стикеры
	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		info := fmt.Sprintf("Текст: %s", text)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   info,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "✏️ Изменить текст", CallbackData: "edit:" + st.StickerID}},
				},
			},
		})
	}

	// Навигация
	var navButtons []models.InlineKeyboardButton
	if page > 1 {
		navButtons = append(navButtons, models.InlineKeyboardButton{Text: "◀️", CallbackData: fmt.Sprintf("list:%d", page-1)})
	}
	navButtons = append(navButtons, models.InlineKeyboardButton{Text: fmt.Sprintf("%d/%d", page, totalPages), CallbackData: "list:noop"})
	if page < totalPages {
		navButtons = append(navButtons, models.InlineKeyboardButton{Text: "▶️", CallbackData: fmt.Sprintf("list:%d", page+1)})
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("📋 Стикеры (всего: %d)", total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
				{{Text: "◀️ В меню", CallbackData: "menu:main"}},
			},
		},
	})
}

func (b *Bot) handleListCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	pageStr := strings.TrimPrefix(update.CallbackQuery.Data, "list:")
	if pageStr == "noop" {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
		})
		return
	}

	page, _ := strconv.Atoi(pageStr)
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

func (b *Bot) handleHelp(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := `Как добавить стикеры:
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

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
}

func (b *Bot) handleStats(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	count, err := b.repo.GetUserStickerCount(update.Message.From.ID)
	if err != nil {
		log.Printf("Error getting stats: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при получении статистики",
		})
		return
	}

	msg := fmt.Sprintf("У тебя сохранено стикеров: %d", count)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
}

func (b *Bot) handleList(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	// Парсим номер страницы из команды /list или /list 2
	pageStr := strings.TrimPrefix(update.Message.Text, "/list")
	pageStr = strings.TrimSpace(pageStr)
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	const perPage = 5
	offset := (page - 1) * perPage

	// Получаем общее количество
	total, err := b.repo.GetUserStickerCount(userID)
	if err != nil {
		log.Printf("Error getting sticker count: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Ошибка при получении списка",
		})
		return
	}

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "У тебя пока нет сохранённых стикеров.\n\nОтправь мне стикер или используй /addpack <имя_пака>",
		})
		return
	}

	// Получаем стикеры
	stickers, err := b.repo.GetUserStickers(userID, perPage, offset)
	if err != nil {
		log.Printf("Error getting stickers: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Ошибка при получении списка",
		})
		return
	}

	if len(stickers) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Страница %d пуста. Всего стикеров: %d", page, total),
		})
		return
	}

	totalPages := (total + perPage - 1) / perPage

	// Отправляем каждый стикер с его текстом
	for _, st := range stickers {
		text := st.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		// Отправляем стикер
		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: st.FileID},
		})

		// Отправляем информацию о стикере
		info := fmt.Sprintf("Текст: %s", text)
		if st.SetName != "" {
			info += fmt.Sprintf("\nПак: %s", st.SetName)
		}

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   info,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "✏️ Изменить текст", CallbackData: "edit:" + st.StickerID}},
				},
			},
		})
	}

	// Навигация по страницам
	var navText string
	if totalPages > 1 {
		navText = fmt.Sprintf("Страница %d из %d (всего %d стикеров)\n\n", page, totalPages, total)
		if page > 1 {
			navText += fmt.Sprintf("/list %d — предыдущая\n", page-1)
		}
		if page < totalPages {
			navText += fmt.Sprintf("/list %d — следующая", page+1)
		}
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   navText,
		})
	}
}

func (b *Bot) handleSettings(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	currentEngine := b.repo.GetUserOCREngine(userID)

	engines := []struct {
		name  string
		label string
	}{
		{"paddle", "PaddleOCR"},
		{"easy", "EasyOCR"},
		{"api", "OCR.space API"},
		{"tesseract", "Tesseract"},
	}

	var buttons [][]models.InlineKeyboardButton
	for _, e := range engines {
		label := e.label
		if e.name == currentEngine {
			label = "✓ " + label
		}
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: label, CallbackData: "ocr:" + e.name},
		})
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Текущий OCR движок: %s\n\nВыбери движок для распознавания текста:", currentEngine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}

func (b *Bot) handleOCRCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	engine := strings.TrimPrefix(update.CallbackQuery.Data, "ocr:")
	userID := update.CallbackQuery.From.ID

	// Проверяем валидность движка
	validEngines := map[string]string{
		"paddle":    "PaddleOCR",
		"easy":      "EasyOCR",
		"api":       "OCR.space API",
		"tesseract": "Tesseract",
	}

	engineLabel, ok := validEngines[engine]
	if !ok {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Неизвестный движок",
			ShowAlert:       true,
		})
		return
	}

	// Сохраняем выбор
	if err := b.repo.SetUserOCREngine(userID, engine); err != nil {
		log.Printf("Error saving OCR engine: %v", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка сохранения",
			ShowAlert:       true,
		})
		return
	}

	// Обновляем кнопки
	engines := []struct {
		name  string
		label string
	}{
		{"paddle", "PaddleOCR"},
		{"easy", "EasyOCR"},
		{"api", "OCR.space API"},
		{"tesseract", "Tesseract"},
	}

	var buttons [][]models.InlineKeyboardButton
	for _, e := range engines {
		label := e.label
		if e.name == engine {
			label = "✓ " + label
		}
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: label, CallbackData: "ocr:" + e.name},
		})
	}

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      fmt.Sprintf("Текущий OCR движок: %s\n\nВыбери движок для распознавания текста:", engine),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            fmt.Sprintf("Выбран: %s", engineLabel),
	})
}

func (b *Bot) handleSelectOCRCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	// Format: selectocr:stickerID:engine
	data := strings.TrimPrefix(update.CallbackQuery.Data, "selectocr:")
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	stickerID := parts[0]
	engine := parts[1]
	userID := update.CallbackQuery.From.ID

	// Получаем сохраненные результаты
	b.pendingOCRMu.RLock()
	results, ok := b.pendingOCR[stickerID]
	b.pendingOCRMu.RUnlock()

	if !ok {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Результаты устарели, отправь стикер заново",
			ShowAlert:       true,
		})
		return
	}

	text := results[engine]

	// Сохраняем выбранный текст
	if err := b.repo.UpdateStickerText(userID, stickerID, text); err != nil {
		log.Printf("Error updating sticker text: %v", err)
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка сохранения",
			ShowAlert:       true,
		})
		return
	}

	// Удаляем из pending
	b.pendingOCRMu.Lock()
	delete(b.pendingOCR, stickerID)
	b.pendingOCRMu.Unlock()

	// Обновляем сообщение
	engineLabels := map[string]string{
		"paddle":    "PaddleOCR",
		"easy":      "EasyOCR",
		"api":       "OCR.space",
		"tesseract": "Tesseract",
	}

	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      fmt.Sprintf("✅ Сохранено!\n\nДвижок: %s\nТекст: \"%s\"", engineLabels[engine], text),
	})

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Сохранено!",
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

	stickers, err := b.repo.SearchByText(update.Message.From.ID, query)
	if err != nil {
		log.Printf("Error searching: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при поиске",
		})
		return
	}

	if len(stickers) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Стикеров с текстом '%s' не найдено", query),
		})
		return
	}

	// Отправляем найденные стикеры (максимум 10)
	limit := 10
	if len(stickers) < limit {
		limit = len(stickers)
	}

	for i := 0; i < limit; i++ {
		tgBot.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  update.Message.Chat.ID,
			Sticker: &models.InputFileString{Data: stickers[i].FileID},
		})
	}

	if len(stickers) > 10 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Показано 10 из %d найденных стикеров", len(stickers)),
		})
	}
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

	userID := update.Message.From.ID
	ocrEngine := b.repo.GetUserOCREngine(userID)

	// Проверяем, нет ли уже активной индексации
	b.activeIndexingMu.RLock()
	_, exists := b.activeIndexing[userID]
	b.activeIndexingMu.RUnlock()
	if exists {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "У тебя уже идёт индексация. Дождись завершения или отмени её.",
		})
		return
	}

	// Получаем стикер-пак
	stickerSet, err := tgBot.GetStickerSet(ctx, &bot.GetStickerSetParams{Name: setName})
	if err != nil {
		log.Printf("Error getting sticker set: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Не удалось найти стикер-пак '%s'. Проверь правильность имени.", setName),
		})
		return
	}

	total := len(stickerSet.Stickers)
	chatID := update.Message.Chat.ID

	// Создаём контекст с отменой
	indexCtx, cancel := context.WithCancel(ctx)

	// Сохраняем функцию отмены
	b.activeIndexingMu.Lock()
	b.activeIndexing[userID] = cancel
	b.activeIndexingMu.Unlock()

	// Cleanup при завершении
	defer func() {
		b.activeIndexingMu.Lock()
		delete(b.activeIndexing, userID)
		b.activeIndexingMu.Unlock()
	}()

	// Отправляем начальное сообщение с прогрессом и кнопкой отмены
	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Индексирую пак \"%s\"\n%s 0%%", stickerSet.Title, progressBar(0, total)),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
			},
		},
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	// Многопоточная обработка
	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool

	const workers = 5
	jobs := make(chan models.Sticker, workers)
	var wg sync.WaitGroup

	// Запускаем воркеры с задержкой
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*100) * time.Millisecond)

			for sticker := range jobs {
				// Проверяем отмену
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					return
				default:
				}

				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				text, _ := b.downloadAndOCR(indexCtx, fileURL, ocrEngine)

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
				}

				if err := b.repo.SaveSticker(s); err == nil {
					processed.Add(1)
					if text != "" {
						withText.Add(1)
					}
				}
				completed.Add(1)
			}
		}(w)
	}

	// Отправляем задачи в горутине
	go func() {
		for _, sticker := range stickerSet.Stickers {
			select {
			case <-indexCtx.Done():
				close(jobs)
				return
			case jobs <- sticker:
			}
		}
		close(jobs)
	}()

	// Обновляем прогресс пока воркеры работают
	go func() {
		lastUpdate := int64(0)
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= int64(total) {
				break
			}
			if current-lastUpdate >= 3 {
				percent := current * 100 / int64(total)
				tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatID,
					MessageID: progressMsg.ID,
					Text:      fmt.Sprintf("Индексирую пак \"%s\"\n%s %d%%\n\nОбработано: %d/%d", stickerSet.Title, progressBar(int(current), total), percent, current, total),
					ReplyMarkup: &models.InlineKeyboardMarkup{
						InlineKeyboard: [][]models.InlineKeyboardButton{
							{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
						},
					},
				})
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Ждём завершения всех воркеров
	wg.Wait()

	// Финальное сообщение
	if cancelled.Load() {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", stickerSet.Title, processed.Load()),
		})
	} else {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("✅ Пак \"%s\" добавлен!\n\nСтикеров: %d\nС текстом: %d", stickerSet.Title, processed.Load(), withText.Load()),
		})
	}
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

	// Получаем последний стикер пользователя
	b.lastStickerMu.RLock()
	stickerID, ok := b.lastSticker[userID]
	b.lastStickerMu.RUnlock()

	if !ok || stickerID == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Сначала отправь стикер, текст которого хочешь исправить.",
		})
		return
	}

	// Обновляем текст
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

func (b *Bot) handleAddPackCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	setName := strings.TrimPrefix(update.CallbackQuery.Data, "addpack:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	ocrEngine := b.repo.GetUserOCREngine(userID)

	// Проверяем, нет ли уже активной индексации
	b.activeIndexingMu.RLock()
	_, exists := b.activeIndexing[userID]
	b.activeIndexingMu.RUnlock()
	if exists {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "У тебя уже идёт индексация",
			ShowAlert:       true,
		})
		return
	}

	// Отвечаем на callback чтобы убрать "часики"
	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Начинаю индексацию...",
	})

	// Получаем стикер-пак
	stickerSet, err := tgBot.GetStickerSet(ctx, &bot.GetStickerSetParams{Name: setName})
	if err != nil {
		log.Printf("Error getting sticker set: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось найти стикер-пак '%s'", setName),
		})
		return
	}

	total := len(stickerSet.Stickers)

	// Создаём контекст с отменой
	indexCtx, cancel := context.WithCancel(ctx)

	// Сохраняем функцию отмены
	b.activeIndexingMu.Lock()
	b.activeIndexing[userID] = cancel
	b.activeIndexingMu.Unlock()

	// Cleanup при завершении
	defer func() {
		b.activeIndexingMu.Lock()
		delete(b.activeIndexing, userID)
		b.activeIndexingMu.Unlock()
	}()

	// Отправляем начальное сообщение с прогрессом и кнопкой отмены
	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Индексирую пак \"%s\"\n%s 0%%", stickerSet.Title, progressBar(0, total)),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
			},
		},
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	// Многопоточная обработка
	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool

	const workers = 5
	jobs := make(chan models.Sticker, workers)
	var wg sync.WaitGroup

	// Запускаем воркеры с задержкой
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*100) * time.Millisecond)

			for sticker := range jobs {
				// Проверяем отмену
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					return
				default:
				}

				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				text, _ := b.downloadAndOCR(indexCtx, fileURL, ocrEngine)

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
				}

				if err := b.repo.SaveSticker(s); err == nil {
					processed.Add(1)
					if text != "" {
						withText.Add(1)
					}
				}
				completed.Add(1)
			}
		}(w)
	}

	// Отправляем задачи в горутине
	go func() {
		for _, sticker := range stickerSet.Stickers {
			select {
			case <-indexCtx.Done():
				close(jobs)
				return
			case jobs <- sticker:
			}
		}
		close(jobs)
	}()

	// Обновляем прогресс пока воркеры работают
	go func() {
		lastUpdate := int64(0)
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= int64(total) {
				break
			}
			if current-lastUpdate >= 3 {
				percent := current * 100 / int64(total)
				tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatID,
					MessageID: progressMsg.ID,
					Text:      fmt.Sprintf("Индексирую пак \"%s\"\n%s %d%%\n\nОбработано: %d/%d", stickerSet.Title, progressBar(int(current), total), percent, current, total),
					ReplyMarkup: &models.InlineKeyboardMarkup{
						InlineKeyboard: [][]models.InlineKeyboardButton{
							{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
						},
					},
				})
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Ждём завершения всех воркеров
	wg.Wait()

	// Финальное сообщение
	if cancelled.Load() {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", stickerSet.Title, processed.Load()),
		})
	} else {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("✅ Пак \"%s\" добавлен!\n\nСтикеров: %d\nС текстом: %d", stickerSet.Title, processed.Load(), withText.Load()),
		})
	}
}

func (b *Bot) handleEditCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	stickerID := strings.TrimPrefix(update.CallbackQuery.Data, "edit:")
	userID := update.CallbackQuery.From.ID
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	// Сохраняем sticker ID для редактирования
	b.lastStickerMu.Lock()
	b.lastSticker[userID] = stickerID
	b.lastStickerMu.Unlock()

	// Ставим флаг ожидания текста
	b.awaitingEditMu.Lock()
	b.awaitingEdit[userID] = true
	b.awaitingEditMu.Unlock()

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Введи правильный текст для этого стикера:",
	})
}

func (b *Bot) handleCancelCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	targetUserIDStr := strings.TrimPrefix(update.CallbackQuery.Data, "cancel:")
	targetUserID, err := strconv.ParseInt(targetUserIDStr, 10, 64)
	if err != nil {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка",
		})
		return
	}

	callerUserID := update.CallbackQuery.From.ID

	// Проверяем, что отменяет тот же пользователь
	if callerUserID != targetUserID {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ты не можешь отменить чужую индексацию",
			ShowAlert:       true,
		})
		return
	}

	// Ищем и вызываем функцию отмены
	b.activeIndexingMu.RLock()
	cancel, exists := b.activeIndexing[targetUserID]
	b.activeIndexingMu.RUnlock()

	if !exists {
		tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Индексация уже завершена",
		})
		return
	}

	// Отменяем
	cancel()

	tgBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Отменяю...",
	})
}

func (b *Bot) defaultHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	// Обработка стикера
	if update.Message.Sticker != nil {
		b.handleSticker(ctx, tgBot, update)
		return
	}

	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	// Обработка кнопок меню
	switch text {
	case "🔍 Поиск":
		b.awaitingSearchMu.Lock()
		b.awaitingSearch[userID] = true
		b.awaitingSearchMu.Unlock()
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "🔍 Введи текст для поиска:",
		})
		return

	case "📦 Добавить пак":
		b.awaitingAddpackMu.Lock()
		b.awaitingAddpack[userID] = true
		b.awaitingAddpackMu.Unlock()
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📦 Введи имя стикер-пака:\n\nИмя пака можно узнать, отправив мне любой стикер из него.",
		})
		return

	case "📋 Мои стикеры":
		b.sendStickerListMsg(ctx, tgBot, chatID, userID, 1)
		return

	case "⚙️ Настройки":
		b.sendSettingsMsg(ctx, tgBot, chatID, userID)
		return

	case "📊 Статистика":
		count, _ := b.repo.GetUserStickerCount(userID)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("📊 Статистика\n\nСохранено стикеров: %d", count),
		})
		return

	case "❓ Помощь":
		msg := `❓ Помощь

Как добавить стикеры:
• Отправь мне стикер — добавлю один
• Нажми "📦 Добавить пак" — добавлю весь пак

Как искать:
• Нажми "🔍 Поиск" и введи текст
• Или просто напиши текст — тоже найду!

Как исправить текст:
• В списке стикеров нажми "✏️ Изменить текст"`
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   msg,
		})
		return
	}

	// Проверяем режим ожидания редактирования
	b.awaitingEditMu.RLock()
	awaitingEdit := b.awaitingEdit[userID]
	b.awaitingEditMu.RUnlock()

	if awaitingEdit && text != "" && !strings.HasPrefix(text, "/") {
		b.handleAwaitingEdit(ctx, tgBot, update)
		return
	}

	// Проверяем режим ожидания поиска
	b.awaitingSearchMu.RLock()
	awaitingSearch := b.awaitingSearch[userID]
	b.awaitingSearchMu.RUnlock()

	if awaitingSearch && text != "" && !strings.HasPrefix(text, "/") {
		b.awaitingSearchMu.Lock()
		delete(b.awaitingSearch, userID)
		b.awaitingSearchMu.Unlock()

		b.doSearch(ctx, tgBot, chatID, userID, text)
		return
	}

	// Проверяем режим ожидания добавления пака
	b.awaitingAddpackMu.RLock()
	awaitingAddpack := b.awaitingAddpack[userID]
	b.awaitingAddpackMu.RUnlock()

	if awaitingAddpack && text != "" && !strings.HasPrefix(text, "/") {
		b.awaitingAddpackMu.Lock()
		delete(b.awaitingAddpack, userID)
		b.awaitingAddpackMu.Unlock()

		b.doAddPack(ctx, tgBot, chatID, userID, strings.TrimSpace(text))
		return
	}

	// Обработка текстового поиска без команды
	if text != "" && !strings.HasPrefix(text, "/") {
		b.handleTextSearch(ctx, tgBot, update)
	}
}

func (b *Bot) handleAwaitingEdit(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	newText := strings.TrimSpace(update.Message.Text)

	// Убираем флаг ожидания
	b.awaitingEditMu.Lock()
	delete(b.awaitingEdit, userID)
	b.awaitingEditMu.Unlock()

	// Получаем sticker ID
	b.lastStickerMu.RLock()
	stickerID := b.lastSticker[userID]
	b.lastStickerMu.RUnlock()

	if stickerID == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка: стикер не найден",
		})
		return
	}

	// Обновляем текст
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

func (b *Bot) handleSticker(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	sticker := update.Message.Sticker
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	// Отправляем сообщение о начале распознавания
	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Распознаю текст...",
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	// Скачиваем стикер для OCR
	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
	if err != nil {
		log.Printf("Error getting file: %v", err)
		return
	}

	fileURL := tgBot.FileDownloadLink(file)

	// Запускаем все OCR движки параллельно
	engines := []struct {
		name  string
		label string
	}{
		{"api", "OCR.space"},
		{"paddle", "PaddleOCR"},
		{"easy", "EasyOCR"},
		{"tesseract", "Tesseract"},
	}

	results := make(map[string]string)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	for _, engine := range engines {
		wg.Add(1)
		go func(engineName string) {
			defer wg.Done()
			text, err := b.downloadAndOCR(ctx, fileURL, engineName)
			if err != nil {
				log.Printf("OCR error (%s): %v", engineName, err)
				text = ""
			}
			resultsMu.Lock()
			results[engineName] = text
			resultsMu.Unlock()
		}(engine.name)
	}

	wg.Wait()

	// Логируем результаты
	log.Printf("OCR results for sticker %s:", sticker.FileUniqueID)
	for name, text := range results {
		if text != "" {
			log.Printf("  %s: %q", name, text)
		} else {
			log.Printf("  %s: (empty)", name)
		}
	}

	// Сохраняем результаты для выбора
	b.pendingOCRMu.Lock()
	b.pendingOCR[sticker.FileUniqueID] = results
	b.pendingOCRMu.Unlock()

	// Сохраняем как последний стикер для /edit
	b.lastStickerMu.Lock()
	b.lastSticker[userID] = sticker.FileUniqueID
	b.lastStickerMu.Unlock()

	// Сохраняем стикер в базу (пока без текста)
	s := &repository.Sticker{
		UserID:    userID,
		StickerID: sticker.FileUniqueID,
		SetName:   sticker.SetName,
		FileID:    sticker.FileID,
		Text:      "",
		Emoji:     sticker.Emoji,
	}
	if err := b.repo.SaveSticker(s); err != nil {
		log.Printf("Error saving sticker: %v", err)
	}

	// Формируем сообщение с результатами
	var msgBuilder strings.Builder
	msgBuilder.WriteString("Результаты распознавания:\n\n")

	var buttons [][]models.InlineKeyboardButton
	hasResults := false

	for _, engine := range engines {
		text := results[engine.name]
		if text != "" {
			hasResults = true
			msgBuilder.WriteString(fmt.Sprintf("%s:\n%s\n\n", engine.label, text))
			buttons = append(buttons, []models.InlineKeyboardButton{
				{Text: fmt.Sprintf("✓ %s", engine.label), CallbackData: fmt.Sprintf("selectocr:%s:%s", sticker.FileUniqueID, engine.name)},
			})
		} else {
			msgBuilder.WriteString(fmt.Sprintf("%s:\n(не распознано)\n\n", engine.label))
		}
	}

	if !hasResults {
		msgBuilder.WriteString("Текст не распознан ни одним движком.")
	}

	// Кнопка "Исправить текст"
	buttons = append(buttons, []models.InlineKeyboardButton{
		{Text: "✏️ Ввести вручную", CallbackData: "edit:" + sticker.FileUniqueID},
	})

	// Кнопка "Добавить весь пак" (если есть имя пака)
	if sticker.SetName != "" {
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: "📦 Добавить весь пак", CallbackData: "addpack:" + sticker.SetName},
		})
	}

	_, err = tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: progressMsg.ID,
		Text:      msgBuilder.String(),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
	if err != nil {
		log.Printf("Error editing message: %v", err)
	}
}

func (b *Bot) handleTextSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.Message.Text)
	if len(query) < 2 {
		return
	}

	stickers, err := b.repo.SearchByText(update.Message.From.ID, query)
	if err != nil {
		return
	}

	if len(stickers) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
		})
		return
	}

	// Отправляем первые 5 найденных
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

func (b *Bot) downloadAndOCR(ctx context.Context, fileURL string, engine string) (string, error) {
	// Проверяем отмену перед началом
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Скачиваем файл
	resp, err := http.Get(fileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Создаем временный файл
	tmpFile, err := os.CreateTemp("", "sticker-*.webp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", err
	}
	tmpFile.Close()

	// Конвертируем webp в png для лучшего OCR
	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	// Используем ImageMagick для конвертации
	if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err != nil {
		// Пробуем OCR напрямую на webp
		return b.ocr.RecognizeText(ctx, tmpFile.Name(), engine)
	}

	return b.ocr.RecognizeText(ctx, pngPath, engine)
}

func convertWebPToPNG(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "convert", src, dst)
	return cmd.Run()
}

func progressBar(current, total int) string {
	const barLength = 10
	if total == 0 {
		return "[░░░░░░░░░░]"
	}

	filled := current * barLength / total
	if filled > barLength {
		filled = barLength
	}

	bar := "["
	for i := 0; i < barLength; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	bar += "]"
	return bar
}

func (b *Bot) doSearch(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, query string) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Слишком короткий запрос. Введи минимум 2 символа.",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
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
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
		return
	}

	if len(stickers) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Стикеров с текстом \"%s\" не найдено", query),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "🔍 Искать ещё", CallbackData: "menu:search"}},
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
		return
	}

	// Отправляем найденные стикеры (максимум 10)
	limit := 10
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
	if len(stickers) > 10 {
		msg = fmt.Sprintf("Показано 10 из %d найденных", len(stickers))
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   msg,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "🔍 Искать ещё", CallbackData: "menu:search"}},
				{{Text: "◀️ В меню", CallbackData: "menu:main"}},
			},
		},
	})
}

func (b *Bot) doAddPack(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, setName string) {
	if setName == "" {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Имя пака не может быть пустым",
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
		return
	}

	ocrEngine := b.repo.GetUserOCREngine(userID)

	// Проверяем, нет ли уже активной индексации
	b.activeIndexingMu.RLock()
	_, exists := b.activeIndexing[userID]
	b.activeIndexingMu.RUnlock()
	if exists {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "У тебя уже идёт индексация. Дождись завершения или отмени её.",
		})
		return
	}

	// Получаем стикер-пак
	stickerSet, err := tgBot.GetStickerSet(ctx, &bot.GetStickerSetParams{Name: setName})
	if err != nil {
		log.Printf("Error getting sticker set: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось найти стикер-пак '%s'. Проверь правильность имени.", setName),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "📦 Попробовать снова", CallbackData: "menu:addpack"}},
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
		return
	}

	total := len(stickerSet.Stickers)

	// Создаём контекст с отменой
	indexCtx, cancel := context.WithCancel(ctx)

	// Сохраняем функцию отмены
	b.activeIndexingMu.Lock()
	b.activeIndexing[userID] = cancel
	b.activeIndexingMu.Unlock()

	// Cleanup при завершении
	defer func() {
		b.activeIndexingMu.Lock()
		delete(b.activeIndexing, userID)
		b.activeIndexingMu.Unlock()
	}()

	// Отправляем начальное сообщение с прогрессом и кнопкой отмены
	progressMsg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Индексирую пак \"%s\"\n%s 0%%", stickerSet.Title, progressBar(0, total)),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
			},
		},
	})
	if err != nil {
		log.Printf("Error sending progress message: %v", err)
		return
	}

	// Многопоточная обработка
	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool

	const workers = 5
	jobs := make(chan models.Sticker, workers)
	var wg sync.WaitGroup

	// Запускаем воркеры с задержкой
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*100) * time.Millisecond)

			for sticker := range jobs {
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					return
				default:
				}

				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				text, _ := b.downloadAndOCR(indexCtx, fileURL, ocrEngine)

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
				}

				if err := b.repo.SaveSticker(s); err == nil {
					processed.Add(1)
					if text != "" {
						withText.Add(1)
					}
				}
				completed.Add(1)
			}
		}(w)
	}

	// Отправляем задачи в горутине
	go func() {
		for _, sticker := range stickerSet.Stickers {
			select {
			case <-indexCtx.Done():
				close(jobs)
				return
			case jobs <- sticker:
			}
		}
		close(jobs)
	}()

	// Обновляем прогресс пока воркеры работают
	go func() {
		lastUpdate := int64(0)
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= int64(total) {
				break
			}
			if current-lastUpdate >= 3 {
				percent := current * 100 / int64(total)
				tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
					ChatID:    chatID,
					MessageID: progressMsg.ID,
					Text:      fmt.Sprintf("Индексирую пак \"%s\"\n%s %d%%\n\nОбработано: %d/%d", stickerSet.Title, progressBar(int(current), total), percent, current, total),
					ReplyMarkup: &models.InlineKeyboardMarkup{
						InlineKeyboard: [][]models.InlineKeyboardButton{
							{{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)}},
						},
					},
				})
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Ждём завершения всех воркеров
	wg.Wait()

	// Финальное сообщение
	if cancelled.Load() {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⛔ Индексация пака \"%s\" отменена\n\nУспело сохраниться: %d стикеров", stickerSet.Title, processed.Load()),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
	} else {
		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("✅ Пак \"%s\" добавлен!\n\nСтикеров: %d\nС текстом: %d", stickerSet.Title, processed.Load(), withText.Load()),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "📦 Добавить ещё", CallbackData: "menu:addpack"}},
					{{Text: "◀️ В меню", CallbackData: "menu:main"}},
				},
			},
		})
	}
}
