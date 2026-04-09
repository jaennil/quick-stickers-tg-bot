package ui

import (
	"fmt"

	"github.com/go-telegram/bot/models"
)

func MainMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{{Text: "🔍 Поиск"}, {Text: "📦 Добавить пак"}},
			{{Text: "📋 Мои стикеры"}, {Text: "⚙️ Настройки"}},
			{{Text: "📊 Статистика"}, {Text: "❓ Помощь"}},
		},
		ResizeKeyboard: true,
	}
}

func PaginationButtons(page, totalPages int, callbackPrefix string) []models.InlineKeyboardButton {
	var navButtons []models.InlineKeyboardButton
	if page > 1 {
		navButtons = append(navButtons, models.InlineKeyboardButton{
			Text: "◀️", CallbackData: fmt.Sprintf("%s:%d", callbackPrefix, page-1),
		})
	}
	navButtons = append(navButtons, models.InlineKeyboardButton{
		Text: fmt.Sprintf("%d/%d", page, totalPages), CallbackData: callbackPrefix + ":noop",
	})
	if page < totalPages {
		navButtons = append(navButtons, models.InlineKeyboardButton{
			Text: "▶️", CallbackData: fmt.Sprintf("%s:%d", callbackPrefix, page+1),
		})
	}
	return navButtons
}

func BackButton() []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "◀️ Назад", CallbackData: "menu:main"},
	}
}

func BackToMenuButton() []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "◀️ В меню", CallbackData: "menu:main"},
	}
}

func CancelButton(userID int64) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "❌ Отменить", CallbackData: fmt.Sprintf("cancel:%d", userID)},
	}
}

func EditStickerButton(stickerID string) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "✏️ Изменить", CallbackData: "edit:" + stickerID},
		{Text: "🗑 Удалить", CallbackData: "delete:" + stickerID},
	}
}

func AddPackButton(setName string) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "📦 Добавить весь пак", CallbackData: "addpack:" + setName},
	}
}

func SearchAgainButtons() [][]models.InlineKeyboardButton {
	return [][]models.InlineKeyboardButton{
		{{Text: "🔍 Искать ещё", CallbackData: "menu:search"}},
		BackToMenuButton(),
	}
}

func AddPackAgainButtons() [][]models.InlineKeyboardButton {
	return [][]models.InlineKeyboardButton{
		{{Text: "📦 Добавить ещё", CallbackData: "menu:addpack"}},
		BackToMenuButton(),
	}
}

func TryAgainAddPackButtons() [][]models.InlineKeyboardButton {
	return [][]models.InlineKeyboardButton{
		{{Text: "📦 Попробовать снова", CallbackData: "menu:addpack"}},
		BackToMenuButton(),
	}
}

func EmptyListButtons() [][]models.InlineKeyboardButton {
	return [][]models.InlineKeyboardButton{
		{{Text: "📦 Добавить пак", CallbackData: "menu:addpack"}},
		BackButton(),
	}
}
