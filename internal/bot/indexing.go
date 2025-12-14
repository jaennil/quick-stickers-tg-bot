package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/service"
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

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
