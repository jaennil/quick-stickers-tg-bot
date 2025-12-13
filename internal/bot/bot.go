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
	"strings"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/storage"
)

type Bot struct {
	bot            *bot.Bot
	storage        *storage.Storage
	ocr            *ocr.OCR
	lastSticker    map[int64]string // userID -> stickerID
	lastStickerMu  sync.RWMutex
	awaitingEdit   map[int64]bool // userID -> waiting for edit text
	awaitingEditMu sync.RWMutex
}

func New(token string, storage *storage.Storage, ocr *ocr.OCR) (*Bot, error) {
	b := &Bot{
		storage:      storage,
		ocr:          ocr,
		lastSticker:  make(map[int64]string),
		awaitingEdit: make(map[int64]bool),
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.defaultHandler),
		bot.WithCallbackQueryDataHandler("addpack:", bot.MatchTypePrefix, b.handleAddPackCallback),
		bot.WithCallbackQueryDataHandler("edit:", bot.MatchTypePrefix, b.handleEditCallback),
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
}

func (b *Bot) Start(ctx context.Context) {
	log.Println("Bot started")
	b.bot.Start(ctx)
}

func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := `Привет! Я помогу найти нужный стикер по тексту.

Как использовать:
1. Перешли мне стикер — я распознаю текст и сохраню
2. /addpack <имя_пака> — добавить весь стикер-пак
3. /search <текст> — найду стикеры с этим текстом

Команды:
/help — помощь
/stats — статистика
/search <текст> — поиск
/addpack <имя_пака> — добавить пак
/edit <текст> — исправить текст последнего стикера`

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
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
/edit <правильный текст> — после отправки стикера`

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
}

func (b *Bot) handleStats(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	count, err := b.storage.GetUserStickerCount(update.Message.From.ID)
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

	stickers, err := b.storage.SearchByText(update.Message.From.ID, query)
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
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Начинаю индексацию пака \"%s\" (%d стикеров)...", stickerSet.Title, total),
	})

	userID := update.Message.From.ID
	processed := 0
	withText := 0

	for _, sticker := range stickerSet.Stickers {
		// Скачиваем и распознаем
		file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
		if err != nil {
			log.Printf("Error getting file: %v", err)
			continue
		}

		fileURL := tgBot.FileDownloadLink(file)
		text, err := b.downloadAndOCR(fileURL)
		if err != nil {
			log.Printf("Error OCR: %v", err)
			text = ""
		}

		// Сохраняем
		s := &storage.Sticker{
			UserID:    userID,
			StickerID: sticker.FileUniqueID,
			SetName:   setName,
			FileID:    sticker.FileID,
			Text:      text,
			Emoji:     sticker.Emoji,
		}

		if err := b.storage.SaveSticker(s); err != nil {
			log.Printf("Error saving sticker: %v", err)
			continue
		}

		processed++
		if text != "" {
			withText++
		}
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Готово! Добавлено %d стикеров, текст распознан на %d.", processed, withText),
	})
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
	if err := b.storage.UpdateStickerText(userID, stickerID, newText); err != nil {
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
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Индексирую пак \"%s\" (%d стикеров)...", stickerSet.Title, total),
	})

	processed := 0
	withText := 0

	for _, sticker := range stickerSet.Stickers {
		file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
		if err != nil {
			continue
		}

		fileURL := tgBot.FileDownloadLink(file)
		text, _ := b.downloadAndOCR(fileURL)

		s := &storage.Sticker{
			UserID:    userID,
			StickerID: sticker.FileUniqueID,
			SetName:   setName,
			FileID:    sticker.FileID,
			Text:      text,
			Emoji:     sticker.Emoji,
		}

		if err := b.storage.SaveSticker(s); err == nil {
			processed++
			if text != "" {
				withText++
			}
		}
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Готово! Добавлено %d стикеров, текст распознан на %d.", processed, withText),
	})
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

	// Проверяем режим ожидания редактирования
	b.awaitingEditMu.RLock()
	awaiting := b.awaitingEdit[userID]
	b.awaitingEditMu.RUnlock()

	if awaiting && update.Message.Text != "" && !strings.HasPrefix(update.Message.Text, "/") {
		b.handleAwaitingEdit(ctx, tgBot, update)
		return
	}

	// Обработка текстового поиска без команды
	if update.Message.Text != "" && !strings.HasPrefix(update.Message.Text, "/") {
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
	if err := b.storage.UpdateStickerText(userID, stickerID, newText); err != nil {
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

	// Скачиваем стикер для OCR
	file, err := tgBot.GetFile(ctx, &bot.GetFileParams{FileID: sticker.FileID})
	if err != nil {
		log.Printf("Error getting file: %v", err)
		return
	}

	// Скачиваем файл
	fileURL := tgBot.FileDownloadLink(file)
	text, err := b.downloadAndOCR(fileURL)
	if err != nil {
		log.Printf("Error OCR: %v", err)
		text = "" // Сохраняем без текста
	}

	// Сохраняем в базу
	s := &storage.Sticker{
		UserID:    userID,
		StickerID: sticker.FileUniqueID,
		SetName:   sticker.SetName,
		FileID:    sticker.FileID,
		Text:      text,
		Emoji:     sticker.Emoji,
	}

	if err := b.storage.SaveSticker(s); err != nil {
		log.Printf("Error saving sticker: %v", err)
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Ошибка при сохранении стикера",
		})
		return
	}

	// Сохраняем как последний стикер для /edit
	b.lastStickerMu.Lock()
	b.lastSticker[userID] = sticker.FileUniqueID
	b.lastStickerMu.Unlock()

	var msg string
	if text != "" {
		msg = fmt.Sprintf("Стикер сохранен!\nРаспознанный текст: \"%s\"", text)
	} else {
		msg = "Стикер сохранен!\nТекст не распознан."
	}

	// Создаём inline кнопки
	var buttons [][]models.InlineKeyboardButton

	// Кнопка "Исправить текст"
	buttons = append(buttons, []models.InlineKeyboardButton{
		{Text: "✏️ Исправить текст", CallbackData: "edit:" + sticker.FileUniqueID},
	})

	// Кнопка "Добавить весь пак" (если есть имя пака)
	if sticker.SetName != "" {
		buttons = append(buttons, []models.InlineKeyboardButton{
			{Text: "📦 Добавить весь пак", CallbackData: "addpack:" + sticker.SetName},
		})
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: buttons,
		},
	})
}

func (b *Bot) handleTextSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.Message.Text)
	if len(query) < 2 {
		return
	}

	stickers, err := b.storage.SearchByText(update.Message.From.ID, query)
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

func (b *Bot) downloadAndOCR(fileURL string) (string, error) {
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
	if err := convertWebPToPNG(tmpFile.Name(), pngPath); err != nil {
		// Пробуем OCR напрямую на webp
		return b.ocr.RecognizeText(tmpFile.Name())
	}

	return b.ocr.RecognizeText(pngPath)
}

func convertWebPToPNG(src, dst string) error {
	cmd := exec.Command("convert", src, dst)
	return cmd.Run()
}
