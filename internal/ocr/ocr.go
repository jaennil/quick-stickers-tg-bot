package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type OCR struct {
	apiKeys      []string
	apiKeyIndex  int
	apiKeyMu     sync.Mutex
	googleAPIKey string
	client       *http.Client
}

func New() *OCR {
	// Собираем все токены OCR.space
	apiKeys := []string{
	}

	// Добавляем токен из env если есть
	if envKey := os.Getenv("OCR_API_KEY"); envKey != "" {
		apiKeys = append([]string{envKey}, apiKeys...)
	}

	googleAPIKey := os.Getenv("GOOGLE_VISION_API_KEY")
	return &OCR{
		apiKeys:      apiKeys,
		googleAPIKey: googleAPIKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getNextAPIKey возвращает следующий API ключ (ротация)
func (o *OCR) getNextAPIKey() string {
	o.apiKeyMu.Lock()
	defer o.apiKeyMu.Unlock()

	key := o.apiKeys[o.apiKeyIndex]
	o.apiKeyIndex = (o.apiKeyIndex + 1) % len(o.apiKeys)
	return key
}

type ocrSpaceResponse struct {
	ParsedResults []struct {
		ParsedText string `json:"ParsedText"`
	} `json:"ParsedResults"`
	IsErroredOnProcessing bool   `json:"IsErroredOnProcessing"`
	ErrorMessage          string `json:"ErrorMessage,omitempty"`
}

// RecognizeText распознает текст используя указанный движок
// engine: "paddle", "easy", "api" (ocr.space), "google", "tesseract"
func (o *OCR) RecognizeText(ctx context.Context, imagePath string, engine string) (string, error) {
	// Проверяем отмену
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	switch engine {
	case "api":
		text, err := o.recognizeViaAPI(ctx, imagePath)
		if err == nil && text != "" {
			return text, nil
		}
		// fallback to tesseract
		return o.recognizeViaTesseract(ctx, imagePath)

	case "google":
		text, err := o.recognizeViaGoogle(ctx, imagePath)
		if err == nil && text != "" {
			return text, nil
		}
		return "", err

	case "tesseract":
		return o.recognizeViaTesseract(ctx, imagePath)

	case "easy", "paddle":
		text, err := o.recognizeViaLocalServer(ctx, imagePath, engine)
		if err == nil && text != "" {
			return text, nil
		}
		// fallback to tesseract if server not running
		return o.recognizeViaTesseract(ctx, imagePath)

	default:
		// По умолчанию paddle
		text, err := o.recognizeViaLocalServer(ctx, imagePath, "paddle")
		if err == nil && text != "" {
			return text, nil
		}
		return o.recognizeViaTesseract(ctx, imagePath)
	}
}

type localOCRResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

func (o *OCR) recognizeViaLocalServer(ctx context.Context, imagePath string, engine string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"path": imagePath, "engine": engine})

	req, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:8765/ocr", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result localOCRResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if result.Error != "" {
		return "", fmt.Errorf("local ocr error: %s", result.Error)
	}

	return cleanText(result.Text), nil
}

func (o *OCR) recognizeViaAPI(ctx context.Context, imagePath string) (string, error) {
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
	writer.WriteField("apikey", o.getNextAPIKey())
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

	// Debug: логируем ответ если ошибка парсинга
	var result ocrSpaceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("json parse error: %v, response: %s", err, string(respBody[:min(len(respBody), 500)]))
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
	return len(o.apiKeys) > 0
}

// recognizeViaTesseract - fallback на локальный Tesseract
func (o *OCR) recognizeViaTesseract(ctx context.Context, imagePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "tesseract", imagePath, "stdout",
		"-l", "rus+eng",
		"--psm", "6",
		"--oem", "3",
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return cleanText(stdout.String()), nil
}

// Google Cloud Vision response types
type googleVisionRequest struct {
	Requests []googleVisionImageRequest `json:"requests"`
}

type googleVisionImageRequest struct {
	Image    googleVisionImage     `json:"image"`
	Features []googleVisionFeature `json:"features"`
}

type googleVisionImage struct {
	Content string `json:"content"`
}

type googleVisionFeature struct {
	Type string `json:"type"`
}

type googleVisionResponse struct {
	Responses []struct {
		TextAnnotations []struct {
			Description string `json:"description"`
		} `json:"textAnnotations"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	} `json:"responses"`
}

// recognizeViaGoogle - Google Cloud Vision API
func (o *OCR) recognizeViaGoogle(ctx context.Context, imagePath string) (string, error) {
	if o.googleAPIKey == "" {
		return "", fmt.Errorf("Google Vision API key not set")
	}

	// Читаем файл и кодируем в base64
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	base64Image := base64.StdEncoding.EncodeToString(imageData)

	// Формируем запрос
	reqBody := googleVisionRequest{
		Requests: []googleVisionImageRequest{
			{
				Image: googleVisionImage{Content: base64Image},
				Features: []googleVisionFeature{
					{Type: "TEXT_DETECTION"},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://vision.googleapis.com/v1/images:annotate?key=%s", o.googleAPIKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result googleVisionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("json parse error: %v, body: %s", err, string(respBody[:min(len(respBody), 500)]))
	}

	if len(result.Responses) == 0 {
		return "", fmt.Errorf("empty response from Google Vision: %s", string(respBody[:min(len(respBody), 500)]))
	}

	if result.Responses[0].Error != nil {
		return "", fmt.Errorf("Google Vision error: %s", result.Responses[0].Error.Message)
	}

	if len(result.Responses[0].TextAnnotations) == 0 {
		return "", nil // Текст не найден
	}

	// Первый элемент содержит весь текст
	return cleanText(result.Responses[0].TextAnnotations[0].Description), nil
}
