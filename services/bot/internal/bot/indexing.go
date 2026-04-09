package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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

	if report.AlreadyIndexed > 0 {
		text += fmt.Sprintf("• Пропущено (уже с текстом): %d\n", report.AlreadyIndexed)
	}
	if report.SkippedManual > 0 {
		text += fmt.Sprintf("• Пропущено (ручное редактирование): %d\n", report.SkippedManual)
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
	b.state.SetPackSize(setName, total)
	logger.Log.Infow("[ADDPACK] found pack", "pack", setName, "title", stickerSet.Title, "stickers", total, "user", userID)

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

	result := b.indexer.IndexPack(indexCtx, tgBot, stickerSet, userID, func(p service.IndexProgress) {
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
		logger.Log.Warnw("[ADDPACK] quota exceeded", "pack", setName, "user", userID, "processed", result.Processed, "total", result.Total)
		remaining := result.Total - int(result.Current)
		if remaining < 0 {
			remaining = 0
		}

		tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: progressMsg.ID,
			Text:      fmt.Sprintf("⚠️ Квота OCR.space исчерпана.\n\nОбработано: %d/%d стикеров\nОсталось: %d\n\nПовтори индексацию позже.", result.Processed, result.Total, remaining),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: ui.AddPackAgainButtons(),
			},
		})
	} else {
		logger.Log.Infow("[ADDPACK] completed", "pack", setName, "user", userID,
			"processed", result.Processed,
			"with_text", result.WithText,
			"already_indexed", result.Report.AlreadyIndexed,
			"skipped_manual", result.Report.SkippedManual,
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
