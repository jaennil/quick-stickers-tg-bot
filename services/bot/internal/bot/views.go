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
	"github.com/jaennil/sticker-search-bot/internal/ui"
)

func (b *Bot) sendMainMenu(ctx context.Context, tgBot *bot.Bot, chatID int64) {
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        mainMenuText,
		ReplyMarkup: ui.MainMenuKeyboard(),
	})
}

func (b *Bot) buildSettingsText() string {
	text := "⚙️ Настройки\n\n"
	text += fmt.Sprintf("Текущий OCR: %s\n", constants.DefaultOCREngine.Label)
	text += constants.DefaultOCREngine.Desc + "\n\n"
	text += "Другие OCR-движки отключены. Если позже появится адекватный вариант, добавим его отдельным провайдером."
	return text
}

func (b *Bot) sendSettingsMsg(ctx context.Context, tgBot *bot.Bot, chatID int64) {
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   b.buildSettingsText(),
	})
}

func (b *Bot) sendSettings(ctx context.Context, tgBot *bot.Bot, chatID int64, messageID int) {
	tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      b.buildSettingsText(),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{ui.BackButton()},
		},
	})
}

func (b *Bot) sendStickerListMsg(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, page int) {
	total, _ := b.repo.GetUserStickerCount(userID)
	photoCount, _ := b.repo.GetUserMediaCount(userID, repository.MediaTypePhoto)
	videoCount, _ := b.repo.GetUserMediaCount(userID, repository.MediaTypeVideo)
	gifCount, _ := b.repo.GetUserMediaCount(userID, repository.MediaTypeGIF)
	stickerCount := total - photoCount - videoCount - gifCount

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "📋 У тебя пока нет сохранённых медиа.\n\nОтправь мне стикер, картинку, видео или добавь целый пак!",
		})
		return
	}

	// Show pack statistics
	packStats, err := b.repo.GetUserPackStats(userID)
	if err != nil {
		logger.Log.Errorw("[LIST] error getting pack stats", "user", userID, "error", err)
	}

	var msgBuilder strings.Builder
	msgBuilder.WriteString(fmt.Sprintf("📋 Мои медиа (всего: %d)\n", total))
	if stickerCount > 0 {
		msgBuilder.WriteString(fmt.Sprintf("  🎭 Стикеров: %d\n", stickerCount))
	}
	if photoCount > 0 {
		msgBuilder.WriteString(fmt.Sprintf("  🖼 Картинок: %d\n", photoCount))
	}
	if videoCount > 0 {
		msgBuilder.WriteString(fmt.Sprintf("  🎬 Видео: %d\n", videoCount))
	}
	if gifCount > 0 {
		msgBuilder.WriteString(fmt.Sprintf("  🎞 GIF: %d\n", gifCount))
	}
	msgBuilder.WriteString("\n")

	var buttons [][]models.InlineKeyboardButton

	if len(packStats) > 0 {
		msgBuilder.WriteString("📦 Паки:\n\n")
		for _, ps := range packStats {
			msgBuilder.WriteString(fmt.Sprintf("• %s — %d шт.\n", ps.SetName, ps.Total))
			if ps.ManualEdited > 0 {
				msgBuilder.WriteString(fmt.Sprintf("  ✏️ ручные: %d\n", ps.ManualEdited))
			}
			msgBuilder.WriteString("\n")

			// Add button for this pack
			buttons = append(buttons, []models.InlineKeyboardButton{
				{Text: fmt.Sprintf("📦 %s (%d)", ps.SetName, ps.Total), CallbackData: fmt.Sprintf("pack:%s:1", ps.SetName)},
			})
		}
	}

	if stickerCount > 0 {
		buttons = append(buttons, []models.InlineKeyboardButton{{Text: fmt.Sprintf("🎭 Стикеры (%d)", stickerCount), CallbackData: "media:sticker:1"}})
	}
	if photoCount > 0 {
		buttons = append(buttons, []models.InlineKeyboardButton{{Text: fmt.Sprintf("🖼 Картинки (%d)", photoCount), CallbackData: "media:photo:1"}})
	}
	if videoCount > 0 {
		buttons = append(buttons, []models.InlineKeyboardButton{{Text: fmt.Sprintf("🎬 Видео (%d)", videoCount), CallbackData: "media:video:1"}})
	}
	if gifCount > 0 {
		buttons = append(buttons, []models.InlineKeyboardButton{{Text: fmt.Sprintf("🎞 GIF (%d)", gifCount), CallbackData: "media:gif:1"}})
	}
	buttons = append(buttons, []models.InlineKeyboardButton{{Text: "📜 Все медиа", CallbackData: "allstickers:1"}})
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

		if err := sendStoredMedia(ctx, tgBot, chatID, st); err != nil {
			logger.Log.Warnw("[LIST] failed to send media", "media", st.StickerID, "error", err)
		}

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

func (b *Bot) sendMediaByType(ctx context.Context, tgBot *bot.Bot, chatID int64, userID int64, mediaType repository.MediaType, page int) {
	offset := (page - 1) * constants.PerPage
	total, _ := b.repo.GetUserMediaCount(userID, mediaType)

	typeName := "🎭 Стикеры"
	switch mediaType {
	case repository.MediaTypePhoto:
		typeName = "🖼 Картинки"
	case repository.MediaTypeVideo:
		typeName = "🎬 Видео"
	case repository.MediaTypeGIF:
		typeName = "🎞 GIF"
	}

	if total == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("%s не найдены.", typeName),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "◀️ К списку медиа", CallbackData: "menu:list"}},
				},
			},
		})
		return
	}

	media, _ := b.repo.GetUserMediaByType(userID, mediaType, constants.PerPage, offset)
	totalPages := (total + constants.PerPage - 1) / constants.PerPage

	for _, m := range media {
		text := m.Text
		if text == "" {
			text = "(текст не распознан)"
		}

		var infoLine string
		if m.ManualEdit {
			infoLine = "✏️ отредактировано"
		} else if m.OCREngine != "" {
			infoLine = fmt.Sprintf("🔍 %s", constants.GetEngineLabel(m.OCREngine))
		}

		if err := sendStoredMedia(ctx, tgBot, chatID, m); err != nil {
			logger.Log.Warnw("[LIST] failed to send media", "media", m.StickerID, "error", err)
		}

		msgText := fmt.Sprintf("Текст: %s", text)
		if infoLine != "" {
			msgText += fmt.Sprintf("\n%s", infoLine)
		}

		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   msgText,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{ui.EditStickerButton(m.StickerID)},
			},
		})
	}

	callbackPrefix := fmt.Sprintf("media:%s", mediaType)
	navButtons := ui.PaginationButtons(page, totalPages, callbackPrefix)
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("%s (страница %d/%d, всего: %d)", typeName, page, totalPages, total),
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				navButtons,
				{{Text: "◀️ К списку медиа", CallbackData: "menu:list"}},
			},
		},
	})
}
