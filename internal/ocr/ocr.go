package ocr

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type OCR struct{}

func New() *OCR {
	return &OCR{}
}

// RecognizeText распознает текст на изображении через Tesseract
func (o *OCR) RecognizeText(ctx context.Context, imagePath string) (string, error) {
	// Предобработка изображения для лучшего распознавания
	processedPath := imagePath + "_processed.png"
	defer os.Remove(processedPath)

	if err := preprocessImage(ctx, imagePath, processedPath); err != nil {
		// Если предобработка не удалась, пробуем с оригиналом
		processedPath = imagePath
	}

	// Проверяем отмену
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Пробуем сначала только русский (меньше путаницы с латиницей)
	results := []string{}

	// PSM 6 - единый блок текста, только русский
	if text := runTesseractLang(ctx, processedPath, "6", "rus"); text != "" {
		results = append(results, text)
	}

	// Проверяем отмену
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// PSM 11 - разреженный текст, только русский
	if text := runTesseractLang(ctx, processedPath, "11", "rus"); text != "" {
		results = append(results, text)
	}

	// Проверяем отмену
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// PSM 6 - с русским и английским (для смешанного текста)
	if text := runTesseractLang(ctx, processedPath, "6", "rus+eng"); text != "" {
		results = append(results, text)
	}

	// Выбираем лучший результат (самый длинный осмысленный)
	bestResult := ""
	for _, r := range results {
		cleaned := cleanText(r)
		if len(cleaned) > len(bestResult) {
			bestResult = cleaned
		}
	}

	return bestResult, nil
}

func runTesseractLang(ctx context.Context, imagePath, psm, lang string) string {
	cmd := exec.CommandContext(ctx, "tesseract", imagePath, "stdout",
		"-l", lang,
		"--psm", psm,
		"--oem", "3",
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return ""
	}

	return stdout.String()
}

func preprocessImage(ctx context.Context, src, dst string) error {
	// ImageMagick: увеличиваем, убираем фон, бинаризация
	cmd := exec.CommandContext(ctx, "convert", src,
		"-resize", "300%",              // Увеличиваем
		"-colorspace", "gray",          // Градации серого
		"-normalize",                   // Нормализация яркости
		"-threshold", "50%",            // Бинаризация (чёрно-белое)
		"-morphology", "Close", "Square:1", // Убираем шум
		dst,
	)
	return cmd.Run()
}

func cleanText(text string) string {
	// Убираем лишние символы
	text = strings.TrimSpace(text)

	// Убираем переносы строк, заменяем на пробелы
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

	// Убираем множественные пробелы
	spaceRegex := regexp.MustCompile(`\s+`)
	text = spaceRegex.ReplaceAllString(text, " ")

	// Убираем мусорные символы (оставляем буквы, цифры, базовую пунктуацию)
	cleanRegex := regexp.MustCompile(`[^a-zA-Zа-яА-ЯёЁ0-9\s\-.,!?]`)
	text = cleanRegex.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}

// IsAvailable проверяет доступен ли Tesseract
func (o *OCR) IsAvailable() bool {
	cmd := exec.Command("tesseract", "--version")
	return cmd.Run() == nil
}
