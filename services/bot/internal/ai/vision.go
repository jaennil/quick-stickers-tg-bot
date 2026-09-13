package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DescribePrompt asks for a verbatim transcription plus one line of visual
// context, so that media whose text is unreadable - or absent - is still
// findable by what it depicts.
const DescribePrompt = "Выпиши дословно весь текст с картинки, сохраняя порядок строк. " +
	"Затем добавь с новой строки короткое описание того, что изображено. " +
	"Только результат, без пояснений."

type Describer interface {
	Describe(ctx context.Context, media []byte, mimeType string) (string, error)
	VisionModel() string
}

type VisionClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	model      string
}

func NewVisionClient(baseURL, token, model string) *VisionClient {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultVisionModel
	}
	return &VisionClient{
		httpClient: &http.Client{Timeout: 3 * time.Minute},
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		model:      model,
	}
}

func (c *VisionClient) VisionModel() string {
	return c.model
}

func (c *VisionClient) Describe(ctx context.Context, media []byte, mimeType string) (string, error) {
	if mimeType == "" {
		mimeType = "image/png"
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(media)
	payload := map[string]any{
		"model":      c.model,
		"max_tokens": 250,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": DescribePrompt},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
			},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return "", fmt.Errorf("vision API returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode vision response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("vision API returned no choices")
	}
	text := strings.TrimSpace(result.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("vision API returned empty text")
	}
	return text, nil
}
