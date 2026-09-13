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

// aiFallbackNote explains that a failed OCR is no longer a dead end, but only
// says so when the vision model is actually configured.
func aiFallbackNote(aiEnabled bool) string {
	if aiEnabled {
		return "\n\nИИ опишет содержимое сам, так что искать получится и без текста. " +
			"Задать текст вручную - кнопка ниже."
	}
	return "\n\nМожешь задать текст вручную кнопкой ниже."
}

func buildOCRResultMessage(mediaLabel string, text string, err error, aiEnabled bool) string {
	switch {
	case errors.Is(err, ocr.ErrQuotaExceeded):
		return fmt.Sprintf("⚠️ OCR.space недоступен: квота исчерпана.\n\nСохранил %s без текста.%s",
			mediaLabel, aiFallbackNote(aiEnabled))
	case err != nil:
		return fmt.Sprintf("⚠️ OCR.space не смог обработать %s.\n\nОшибка: %s%s",
			mediaLabel, err, aiFallbackNote(aiEnabled))
	case text == "":
		return fmt.Sprintf("☁️ OCR.space не нашел текст на %s.%s",
			mediaLabel, aiFallbackNote(aiEnabled))
	default:
		return fmt.Sprintf("☁️ OCR.space:\n%s", text)
	}
}
