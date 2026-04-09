package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/logger"
)

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
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   b.buildSettingsText(),
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
