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

type Provider interface {
	Name() string
	RecognizeText(ctx context.Context, imagePath string) (string, error)
	IsAvailable() bool
}

type OCR struct {
	provider Provider
}

type ocrSpaceProvider struct {
	apiKeys     []string
	apiKeyIndex int
	apiKeyMu    sync.Mutex
	client      *http.Client
}

func New(apiKeys []string, proxyURL string) *OCR {
	return &OCR{
		provider: newOCRSpaceProvider(apiKeys, proxyURL),
	}
}

func newOCRSpaceProvider(apiKeys []string, proxyURL string) Provider {
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

	return &ocrSpaceProvider{
		apiKeys: apiKeys,
		client:  client,
	}
}

type ocrSpaceResponse struct {
	ParsedResults []struct {
		ParsedText string `json:"ParsedText"`
	} `json:"ParsedResults"`
	IsErroredOnProcessing bool   `json:"IsErroredOnProcessing"`
	ErrorMessage          string `json:"ErrorMessage,omitempty"`
}

// RecognizeText uses OCR.space as the only active OCR provider.
func (o *OCR) RecognizeText(ctx context.Context, imagePath string) (string, error) {
	if o.provider == nil {
		return "", fmt.Errorf("no OCR provider configured")
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	return o.provider.RecognizeText(ctx, imagePath)
}

func (o *OCR) IsAvailable() bool {
	return o.provider != nil && o.provider.IsAvailable()
}

func (o *OCR) ProviderName() string {
	if o.provider == nil {
		return ""
	}
	return o.provider.Name()
}

func (p *ocrSpaceProvider) Name() string {
	return constants.OCRSpaceEngineName
}

func (p *ocrSpaceProvider) RecognizeText(ctx context.Context, imagePath string) (string, error) {
	text, err := p.recognizeViaAPI(ctx, imagePath)
	if err != nil {
		return "", err
	}
	return text, nil
}

func (p *ocrSpaceProvider) recognizeViaAPI(ctx context.Context, imagePath string) (string, error) {
	if len(p.apiKeys) == 0 {
		return "", fmt.Errorf("no OCR.space API keys configured")
	}

	// Try all keys until one works
	startKeyIndex := p.getNextKeyIndex()
	triedKeys := 0

	for triedKeys < len(p.apiKeys) {
		keyIndex := (startKeyIndex + triedKeys) % len(p.apiKeys)
		apiKey := p.apiKeys[keyIndex]

		var text string
		var err error
		for attempt := range maxAPIRetries {
			text, err = p.tryAPIKey(ctx, imagePath, apiKey)
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
	logger.Log.Errorw("[OCR] all API keys quota exceeded", "total_keys", len(p.apiKeys))
	return "", ErrQuotaExceeded
}

// getNextKeyIndex returns the next key index for rotation
func (p *ocrSpaceProvider) getNextKeyIndex() int {
	p.apiKeyMu.Lock()
	defer p.apiKeyMu.Unlock()

	idx := p.apiKeyIndex
	p.apiKeyIndex = (p.apiKeyIndex + 1) % len(p.apiKeys)
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

func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return apiKey
	}
	return apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
}

// tryAPIKey attempts OCR with a single API key
func (p *ocrSpaceProvider) tryAPIKey(ctx context.Context, imagePath string, apiKey string) (string, error) {
	maskedKey := maskAPIKey(apiKey)
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
	resp, err := p.client.Do(req)
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
func (p *ocrSpaceProvider) IsAvailable() bool {
	return len(p.apiKeys) > 0
}
