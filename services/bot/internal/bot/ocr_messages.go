package bot

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

func buildDuplicateStickerMessage(existing *repository.Sticker, setName string, packCount int, packTotal int, hasPackTotal bool) string {
	var text strings.Builder

	text.WriteString("♻️ Этот стикер уже есть в базе.\n")
	if setName != "" {
		if hasPackTotal {
			text.WriteString(fmt.Sprintf("📦 Пак %s %d/%d стикеров.\n\n", setName, packCount, packTotal))
		} else {
			text.WriteString(fmt.Sprintf("📦 Пак %s %d стикеров в базе.\n\n", setName, packCount))
		}
	} else {
		text.WriteString("\n")
	}

	infoLine := "ℹ️ Текст еще не задан."
	if existing.Text != "" {
		infoLine = fmt.Sprintf("📝 Текущий текст: %q", existing.Text)
	}
	if existing.ManualEdit {
		infoLine += "\nИсточник: ручное изменение"
	} else if existing.OCREngine != "" {
		infoLine += fmt.Sprintf("\nИсточник: %s", constants.GetEngineLabel(existing.OCREngine))
	}

	text.WriteString(infoLine)
	text.WriteString("\n\nОтправь новый текст следующим сообщением.")

	return text.String()
}

func buildOCRResultMessage(mediaLabel string, text string, err error) string {
	switch {
	case errors.Is(err, ocr.ErrQuotaExceeded):
		return fmt.Sprintf("⚠️ OCR.space недоступен: квота исчерпана.\n\nСохранил %s без текста. Можешь добавить его вручную кнопкой ниже.", mediaLabel)
	case err != nil:
		return fmt.Sprintf("⚠️ OCR.space не смог обработать %s.\n\nОшибка: %s\n\nМожешь задать текст вручную кнопкой ниже.", mediaLabel, err)
	case text == "":
		return fmt.Sprintf("☁️ OCR.space не нашел текст на %s.\n\nМожешь задать его вручную кнопкой ниже.", mediaLabel)
	default:
		return fmt.Sprintf("☁️ OCR.space:\n%s", text)
	}
}
