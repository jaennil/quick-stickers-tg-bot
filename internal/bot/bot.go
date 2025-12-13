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

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/storage"
)

type Bot struct {
	bot     *bot.Bot
	storage *storage.Storage
	ocr     *ocr.OCR
}

func New(token string, storage *storage.Storage, ocr *ocr.OCR) (*Bot, error) {
	b := &Bot{
		storage: storage,
		ocr:     ocr,
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.defaultHandler),
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
}

func (b *Bot) Start(ctx context.Context) {
	log.Println("Bot started")
	b.bot.Start(ctx)
}

func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := `Привет! Я помогу найти нужный стикер по тексту.

Как использовать:
1. Перешли мне стикер - я распознаю текст и сохраню
2. Напиши /search <текст> - найду стикеры с этим текстом

Команды:
/help - помощь
/stats - статистика твоих стикеров
/search <текст> - поиск стикера`

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
}

func (b *Bot) handleHelp(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := `Как добавить стикеры:
- Просто перешли мне любой стикер
- Я распознаю текст на нем и сохраню

Как искать:
/search пятница - найдет стикеры с текстом "пятница"

Чем больше стикеров добавишь, тем лучше поиск!`

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

func (b *Bot) defaultHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	// Обработка стикера
	if update.Message.Sticker != nil {
		b.handleSticker(ctx, tgBot, update)
		return
	}

	// Обработка текстового поиска без команды
	if update.Message.Text != "" && !strings.HasPrefix(update.Message.Text, "/") {
		b.handleTextSearch(ctx, tgBot, update)
	}
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

	var msg string
	if text != "" {
		msg = fmt.Sprintf("Стикер сохранен! Распознанный текст: \"%s\"", text)
	} else {
		msg = "Стикер сохранен! Текст не распознан (возможно его нет на стикере)"
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   msg,
	})
}

func (b *Bot) handleTextSearch(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	query := strings.TrimSpace(update.Message.Text)
	if len(query) < 2 {
		return
	}

	stickers, err := b.storage.SearchByText(update.Message.From.ID, query)
	if err != nil || len(stickers) == 0 {
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
