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
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/logger"
)

type OCR struct {
	apiKeys     []string
	apiKeyIndex int
	apiKeyMu    sync.Mutex
	client      *http.Client
	serverURL   string
}

func New(apiKeys []string, serverURL string) *OCR {
	return &OCR{
		apiKeys:   apiKeys,
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getNextAPIKey возвращает следующий API ключ (ротация)
func (o *OCR) getNextAPIKey() string {
	if len(o.apiKeys) == 0 {
		return ""
	}

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
// engine: "paddle", "easy", "api" (ocr.space), "tesseract"
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
		// fallback to easyocr
		logger.Log.Warnw("[OCR] api failed, falling back to easyocr", "error", err)
		return o.recognizeViaLocalServer(ctx, imagePath, "easy")

	case "tesseract":
		return o.recognizeViaTesseract(ctx, imagePath)

	case "easy", "paddle":
		text, err := o.recognizeViaLocalServer(ctx, imagePath, engine)
		if err == nil && text != "" {
			return text, nil
		}
		// fallback to tesseract if server not running
		logger.Log.Warnw("[OCR] local server failed, falling back to tesseract", "engine", engine, "error", err)
		return o.recognizeViaTesseract(ctx, imagePath)

	default:
		// По умолчанию paddle
		text, err := o.recognizeViaLocalServer(ctx, imagePath, "paddle")
		if err == nil && text != "" {
			return text, nil
		}
		logger.Log.Warnw("[OCR] local server failed, falling back to tesseract", "engine", "paddle", "error", err)
		return o.recognizeViaTesseract(ctx, imagePath)
	}
}

type localOCRResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

func (o *OCR) recognizeViaLocalServer(ctx context.Context, imagePath string, engine string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"path": imagePath, "engine": engine})

	req, err := http.NewRequestWithContext(ctx, "POST", o.serverURL+"/ocr", bytes.NewReader(reqBody))
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
	apiKey := o.getNextAPIKey()
	if apiKey == "" {
		return "", fmt.Errorf("no OCR.space API keys configured")
	}

	// Маскируем ключ для логов
	maskedKey := apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
	logger.Log.Debugw("[OCR] using ocr.space API", "key", maskedKey)

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
	writer.WriteField("apikey", apiKey)
	writer.WriteField("language", "rus")
	writer.WriteField("isOverlayRequired", "false")
	writer.WriteField("OCREngine", "2")

	writer.Close()

	// Создаём запрос
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.ocr.space/parse/image", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	start := time.Now()
	// Отправляем
	resp, err := o.client.Do(req)
	if err != nil {
		logger.Log.Errorw("[OCR] ocr.space request failed", "key", maskedKey, "error", err)
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result ocrSpaceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		logger.Log.Errorw("[OCR] ocr.space invalid response", "key", maskedKey, "response", string(respBody[:min(len(respBody), 200)]))
		return "", fmt.Errorf("json parse error: %v, response: %s", err, string(respBody[:min(len(respBody), 500)]))
	}

	if result.IsErroredOnProcessing {
		logger.Log.Warnw("[OCR] ocr.space error", "key", maskedKey, "error", result.ErrorMessage)
		return "", fmt.Errorf("ocr error: %s", result.ErrorMessage)
	}

	// Собираем текст из всех результатов
	var texts []string
	for _, r := range result.ParsedResults {
		if r.ParsedText != "" {
			texts = append(texts, r.ParsedText)
		}
	}

	text := cleanText(strings.Join(texts, " "))
	logger.Log.Debugw("[OCR] ocr.space success", "key", maskedKey, "duration", time.Since(start), "text_len", len(text))

	return text, nil
}

func cleanText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

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
