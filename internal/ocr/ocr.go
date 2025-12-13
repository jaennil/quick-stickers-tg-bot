package ocr

import (
	"bytes"
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
func (o *OCR) RecognizeText(imagePath string) (string, error) {
	// Предобработка изображения для лучшего распознавания
	processedPath := imagePath + "_processed.png"
	defer os.Remove(processedPath)

	if err := preprocessImage(imagePath, processedPath); err != nil {
		// Если предобработка не удалась, пробуем с оригиналом
		processedPath = imagePath
	}

	// Пробуем разные режимы Tesseract
	results := []string{}

	// PSM 6 - единый блок текста
	if text := runTesseract(processedPath, "6"); text != "" {
		results = append(results, text)
	}

	// PSM 11 - разреженный текст
	if text := runTesseract(processedPath, "11"); text != "" {
		results = append(results, text)
	}

	// PSM 3 - авто
	if text := runTesseract(processedPath, "3"); text != "" {
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

func runTesseract(imagePath, psm string) string {
	cmd := exec.Command("tesseract", imagePath, "stdout",
		"-l", "rus+eng",
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

func preprocessImage(src, dst string) error {
	// ImageMagick: увеличиваем, повышаем контраст, бинаризация
	cmd := exec.Command("convert", src,
		"-resize", "400%",           // Увеличиваем
		"-colorspace", "gray",       // Градации серого
		"-contrast-stretch", "0",    // Контраст
		"-sharpen", "0x1",           // Резкость
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
