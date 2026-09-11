package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const maxErrorBody = 4096

type Embedder interface {
	EmbedText(ctx context.Context, text string) ([]float32, error)
	EmbedMedia(ctx context.Context, text string, media []byte, mimeType string, video bool) ([]float32, error)
	Model() string
}

type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	model      string
	dimensions int
}

func NewClient(baseURL, token, model string, dimensions int) *Client {
	if baseURL == "" {
		baseURL = "https://api.aitunnel.ru/v1"
	}
	if model == "" {
		model = "gemini-embedding-2"
	}
	if dimensions == 0 {
		dimensions = 768
	}
	return &Client{
		httpClient: &http.Client{Timeout: 3 * time.Minute},
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		model:      model,
		dimensions: dimensions,
	}
}

func (c *Client) Model() string {
	return c.model
}

func (c *Client) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return c.embed(ctx, map[string]any{
		"model":      c.model,
		"dimensions": c.dimensions,
		"input":      text,
	})
}

func (c *Client) EmbedMedia(
	ctx context.Context,
	text string,
	media []byte,
	mimeType string,
	video bool,
) ([]float32, error) {
	content := make([]map[string]any, 0, 2)
	if strings.TrimSpace(text) != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(media)
	if video {
		content = append(content, map[string]any{
			"type":        "input_video",
			"input_video": map[string]string{"data": dataURL},
		})
	} else {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": dataURL},
		})
	}
	return c.embed(ctx, map[string]any{
		"model":      c.model,
		"dimensions": c.dimensions,
		"input":      []any{map[string]any{"content": content}},
	})
}

func (c *Client) embed(ctx context.Context, payload any) ([]float32, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return nil, fmt.Errorf("embedding API returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding API returned an empty vector")
	}
	normalize(result.Data[0].Embedding)
	return result.Data[0].Embedding, nil
}

func normalize(vector []float32) {
	var squared float64
	for _, value := range vector {
		squared += float64(value) * float64(value)
	}
	if squared == 0 {
		return
	}
	norm := float32(math.Sqrt(squared))
	for index := range vector {
		vector[index] /= norm
	}
}
