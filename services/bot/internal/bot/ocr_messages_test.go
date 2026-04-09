package bot

import (
	"errors"
	"strings"
	"testing"

	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

func TestBuildDuplicateStickerMessage(t *testing.T) {
	msg := buildDuplicateStickerMessage(&repository.Sticker{
		Text:       "privet",
		OCREngine:  constants.OCRSpaceEngineName,
		ManualEdit: false,
	}, "funny_pack", 12, 42, true)

	for _, want := range []string{
		"Этот стикер уже есть в базе",
		"Пак funny_pack 12/42 стикеров",
		`Текущий текст: "privet"`,
		"Источник: OCR.space",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}

func TestBuildOCRResultMessage(t *testing.T) {
	tests := []struct {
		name string
		text string
		err  error
		want string
	}{
		{
			name: "success",
			text: "hello",
			want: "OCR.space:\nhello",
		},
		{
			name: "empty",
			want: "не нашел текст",
		},
		{
			name: "quota",
			err:  ocr.ErrQuotaExceeded,
			want: "квота исчерпана",
		},
		{
			name: "error",
			err:  errors.New("boom"),
			want: "Ошибка: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := buildOCRResultMessage("стикер", tt.text, tt.err)
			if !strings.Contains(msg, tt.want) {
				t.Fatalf("message %q does not contain %q", msg, tt.want)
			}
		})
	}
}
