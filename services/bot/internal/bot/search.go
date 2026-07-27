package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

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
			Text:   fmt.Sprintf("Медиа с текстом \"%s\" не найдено", query),
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
		if err := sendStoredMedia(ctx, tgBot, chatID, stickers[i]); err != nil {
			logger.Log.Warnw("[SEARCH] failed to send result", "media", stickers[i].StickerID, "error", err)
		}
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
			Text:   fmt.Sprintf("Медиа с текстом \"%s\" не найдено", query),
		})
		return
	}

	logger.Log.Infow("[TEXT] found", "user", userID, "query", query, "count", len(stickers))
	limit := 5
	if len(stickers) < limit {
		limit = len(stickers)
	}

	for i := 0; i < limit; i++ {
		if err := sendStoredMedia(ctx, tgBot, update.Message.Chat.ID, stickers[i]); err != nil {
			logger.Log.Warnw("[TEXT] failed to send result", "media", stickers[i].StickerID, "error", err)
		}
	}
}

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
				results = append(results, cachedInlineMedia(i, stickers[i]))
			}
			logger.Log.Infow("[INLINE] found", "user", userID, "query", query, "total", len(stickers), "returned", limit)
		}
	}

	tgBot.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: update.InlineQuery.ID,
		Results:       results,
		CacheTime:     constants.InlineCacheTime,
		IsPersonal:    true,
	})
}
