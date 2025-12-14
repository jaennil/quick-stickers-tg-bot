package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type OCR struct {
	apiKey string
	client *http.Client
}

func New() *OCR {
	apiKey := os.Getenv("OCR_API_KEY")
	if apiKey == "" {
		apiKey = "helloworld" // тестовый ключ (лимитированный)
	}
	return &OCR{
		apiKey: apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type ocrSpaceResponse struct {
	ParsedResults []struct {
		ParsedText string `json:"ParsedText"`
	} `json:"ParsedResults"`
	IsErroredOnProcessing bool   `json:"IsErroredOnProcessing"`
	ErrorMessage          string `json:"ErrorMessage,omitempty"`
}

// RecognizeText распознает текст на изображении через ocr.space API
func (o *OCR) RecognizeText(ctx context.Context, imagePath string) (string, error) {
	// Проверяем отмену
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Читаем файл
	file, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Создаём multipart форму
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Добавляем файл
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}

	// Добавляем параметры
	writer.WriteField("apikey", o.apiKey)
	writer.WriteField("language", "rus")      // русский + английский
	writer.WriteField("isOverlayRequired", "false")
	writer.WriteField("OCREngine", "2")       // Engine 2 лучше для сложных изображений

	writer.Close()

	// Создаём запрос
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.ocr.space/parse/image", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Отправляем
	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result ocrSpaceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}

	if result.IsErroredOnProcessing {
		return "", fmt.Errorf("ocr error: %s", result.ErrorMessage)
	}

	// Собираем текст из всех результатов
	var texts []string
	for _, r := range result.ParsedResults {
		if r.ParsedText != "" {
			texts = append(texts, r.ParsedText)
		}
	}

	return cleanText(strings.Join(texts, " ")), nil
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

	return strings.TrimSpace(text)
}

// IsAvailable проверяет доступен ли OCR API
func (o *OCR) IsAvailable() bool {
	return o.apiKey != ""
}
