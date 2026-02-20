package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/metrics"
	"golang.org/x/net/proxy"
)

const maxAPIRetries = 3

// ErrQuotaExceeded is returned when OCR.space API quota is exceeded
var ErrQuotaExceeded = errors.New("ocr.space quota exceeded")

type OCR struct {
	apiKeys     []string
	apiKeyIndex int
	apiKeyMu    sync.Mutex
	client      *http.Client
	serverURL   string
	proxyURL    string
}

func New(apiKeys []string, serverURL string, proxyURL string) *OCR {
	client := &http.Client{
		Timeout: constants.HTTPTimeout,
	}

	// Configure proxy if provided
	if proxyURL != "" {
		parsedURL, err := url.Parse(proxyURL)
		if err != nil {
			logger.Log.Warnw("[OCR] invalid proxy URL", "proxy", proxyURL, "error", err)
		} else if parsedURL.Scheme == "socks5" {
			// SOCKS5 proxy
			dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, nil, proxy.Direct)
			if err != nil {
				logger.Log.Warnw("[OCR] failed to create SOCKS5 dialer", "proxy", proxyURL, "error", err)
			} else {
				client.Transport = &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return dialer.Dial(network, addr)
					},
				}
				logger.Log.Infow("[OCR] using SOCKS5 proxy for ocr.space", "proxy", proxyURL)
			}
		} else {
			// HTTP/HTTPS proxy
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(parsedURL),
			}
			logger.Log.Infow("[OCR] using HTTP proxy for ocr.space", "proxy", proxyURL)
		}
	}

	return &OCR{
		apiKeys:   apiKeys,
		serverURL: serverURL,
		proxyURL:  proxyURL,
		client:    client,
	}
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
		if err == nil {
			// Return result even if empty - ocr.space is authoritative
			return text, nil
		}
		// If quota exceeded, propagate error (let caller decide)
		if errors.Is(err, ErrQuotaExceeded) {
			return "", err
		}
		// For other errors (network, etc), return error without fallback
		return "", err

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

	client := &http.Client{Timeout: constants.HTTPTimeout}
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
	if len(o.apiKeys) == 0 {
		return "", fmt.Errorf("no OCR.space API keys configured")
	}

	// Try all keys until one works
	startKeyIndex := o.getNextKeyIndex()
	triedKeys := 0

	for triedKeys < len(o.apiKeys) {
		keyIndex := (startKeyIndex + triedKeys) % len(o.apiKeys)
		apiKey := o.apiKeys[keyIndex]

		var text string
		var err error
		for attempt := range maxAPIRetries {
			text, err = o.tryAPIKey(ctx, imagePath, apiKey)
			if err == nil {
				return text, nil
			}
			if errors.Is(err, ErrQuotaExceeded) {
				break
			}
			if isTransientError(err) {
				delay := time.Duration(attempt+1) * 2 * time.Second
				logger.Log.Warnw("[OCR] transient error, retrying", "attempt", attempt+1, "delay", delay, "error", err)
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(delay):
				}
				continue
			}
			// Non-transient, non-quota error — stop retrying this key
			break
		}
		if err == nil {
			return text, nil
		}

		// If quota exceeded, try next key
		if errors.Is(err, ErrQuotaExceeded) {
			triedKeys++
			continue
		}

		// For other errors, return immediately
		return "", err
	}

	// All keys exhausted
	logger.Log.Errorw("[OCR] all API keys quota exceeded", "total_keys", len(o.apiKeys))
	return "", ErrQuotaExceeded
}

// getNextKeyIndex returns the next key index for rotation
func (o *OCR) getNextKeyIndex() int {
	o.apiKeyMu.Lock()
	defer o.apiKeyMu.Unlock()

	idx := o.apiKeyIndex
	o.apiKeyIndex = (o.apiKeyIndex + 1) % len(o.apiKeys)
	return idx
}

// maxAPIFileSize is the maximum file size for ocr.space API (1MB)
const maxAPIFileSize int64 = 1024 * 1024

// ensureUnderSizeLimit compresses the image if it exceeds maxBytes using ImageMagick resize.
// Returns the path to use (original or compressed) and a cleanup function.
func ensureUnderSizeLimit(ctx context.Context, imagePath string, maxBytes int64) (string, func(), error) {
	noop := func() {}

	info, err := os.Stat(imagePath)
	if err != nil {
		return imagePath, noop, err
	}

	if info.Size() <= maxBytes {
		logger.Log.Debugw("[OCR] image within size limit", "size", info.Size(), "limit", maxBytes)
		return imagePath, noop, nil
	}

	logger.Log.Infow("[OCR] image exceeds size limit, compressing",
		"path", imagePath,
		"size", info.Size(),
		"limit", maxBytes,
	)

	compressed, err := os.CreateTemp("", "ocr-compressed-*.png")
	if err != nil {
		return imagePath, noop, fmt.Errorf("create temp file: %w", err)
	}
	compressed.Close()
	compressedPath := compressed.Name()
	cleanup := func() { os.Remove(compressedPath) }

	for _, pct := range []int{75, 50, 25} {
		cmd := exec.CommandContext(ctx, "convert", imagePath, "-resize", fmt.Sprintf("%d%%", pct), compressedPath)
		if err := cmd.Run(); err != nil {
			cleanup()
			return imagePath, noop, fmt.Errorf("imagemagick resize to %d%%: %w", pct, err)
		}

		info, err = os.Stat(compressedPath)
		if err != nil {
			cleanup()
			return imagePath, noop, err
		}

		logger.Log.Debugw("[OCR] compressed image", "resize_percent", pct, "new_size", info.Size())

		if info.Size() <= maxBytes {
			logger.Log.Infow("[OCR] image compressed successfully",
				"resize_percent", pct,
				"new_size", info.Size(),
			)
			return compressedPath, cleanup, nil
		}
	}

	logger.Log.Warnw("[OCR] image still exceeds limit after max compression", "size", info.Size(), "limit", maxBytes)
	return compressedPath, cleanup, nil
}

// tryAPIKey attempts OCR with a single API key
func (o *OCR) tryAPIKey(ctx context.Context, imagePath string, apiKey string) (string, error) {
	// Маскируем ключ для логов
	maskedKey := apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
	logger.Log.Debugw("[OCR] trying ocr.space API", "key", maskedKey)

	// Сжимаем если > 1MB (ограничение ocr.space)
	imagePath, cleanup, compressErr := ensureUnderSizeLimit(ctx, imagePath, maxAPIFileSize)
	if compressErr != nil {
		logger.Log.Warnw("[OCR] compression failed, using original", "error", compressErr)
	}
	defer cleanup()

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
	duration := time.Since(start).Seconds()
	if err != nil {
		metrics.OCRRequestDuration.WithLabelValues("error").Observe(duration)
		metrics.OCRRequestsTotal.WithLabelValues("error").Inc()
		logger.Log.Errorw("[OCR] ocr.space request failed", "key", maskedKey, "error", err)
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		metrics.OCRRequestDuration.WithLabelValues("error").Observe(duration)
		metrics.OCRRequestsTotal.WithLabelValues("error").Inc()
		return "", err
	}

	// Check for quota exceeded (returns plain string, not JSON)
	respStr := string(respBody)
	if strings.Contains(respStr, "maximum") && strings.Contains(respStr, "times within") {
		metrics.OCRRequestDuration.WithLabelValues("quota_exceeded").Observe(duration)
		metrics.OCRRequestsTotal.WithLabelValues("quota_exceeded").Inc()
		logger.Log.Warnw("[OCR] ocr.space quota exceeded", "key", maskedKey)
		return "", ErrQuotaExceeded
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
	metrics.OCRRequestDuration.WithLabelValues("success").Observe(duration)
	metrics.OCRRequestsTotal.WithLabelValues("success").Inc()
	logger.Log.Debugw("[OCR] ocr.space success", "key", maskedKey, "duration", time.Since(start), "text_len", len(text))

	return text, nil
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "i/o timeout")
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

// CompareEngines runs all available OCR engines on the image and returns results
type CompareResult struct {
	Engine string
	Text   string
	Error  error
}

func (o *OCR) CompareEngines(ctx context.Context, imagePath string) []CompareResult {
	engines := []string{"easy", "paddle", "tesseract"}
	results := make([]CompareResult, len(engines))

	for i, engine := range engines {
		var text string
		var err error

		switch engine {
		case "easy", "paddle":
			text, err = o.recognizeViaLocalServer(ctx, imagePath, engine)
		case "tesseract":
			text, err = o.recognizeViaTesseract(ctx, imagePath)
		}

		results[i] = CompareResult{
			Engine: engine,
			Text:   text,
			Error:  err,
		}
	}

	return results
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
