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
		ChatID: chatID,
		Text:   b.buildSettingsText(currentEngine),
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
