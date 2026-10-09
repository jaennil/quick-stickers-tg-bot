// Package files downloads media from Telegram through the bot API, routed the
// same way everywhere: Telegram is unreachable from this cluster without the
// proxy, and the proxy resolves hostnames itself, sidestepping the dead IPv6.
package files

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/logger"
	"golang.org/x/net/proxy"
)

// NewHTTPClient returns a client that reaches Telegram through proxyURL
// (socks5:// or http://), or directly when it is empty.
func NewHTTPClient(proxyURL string) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if proxyURL == "" {
		return client
	}
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		logger.Log.Warnw("[TG_FILES] invalid proxy URL", "error", err)
		return client
	}
	if parsedURL.Scheme == "socks5" {
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, nil, proxy.Direct)
		if err != nil {
			logger.Log.Warnw("[TG_FILES] failed to create SOCKS5 dialer", "error", err)
			return client
		}
		client.Transport = &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		logger.Log.Info("[TG_FILES] using SOCKS5 proxy for Telegram media")
		return client
	}
	client.Transport = &http.Transport{Proxy: http.ProxyURL(parsedURL)}
	logger.Log.Info("[TG_FILES] using HTTP proxy for Telegram media")
	return client
}

// Client downloads files by their bot-API file_id.
type Client struct {
	http    *http.Client
	token   string
	baseURL string
}

func New(token string, httpClient *http.Client) *Client {
	return &Client{http: httpClient, token: token, baseURL: "https://api.telegram.org"}
}

// Download fetches a file and returns its bytes together with the path Telegram
// stores it under, whose extension tells the container format apart.
func (c *Client) Download(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, error) {
	if c.token == "" {
		return nil, "", fmt.Errorf("telegram token is empty")
	}
	lookup := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", c.baseURL, c.token, url.QueryEscape(fileID))
	filePath, err := c.resolve(ctx, lookup)
	if err != nil {
		return nil, "", err
	}

	download := fmt.Sprintf("%s/file/bot%s/%s", c.baseURL, c.token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return data, filePath, nil
}

func (c *Client) resolve(ctx context.Context, lookup string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookup, nil)
	if err != nil {
		return "", err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	if !result.OK || result.Result.FilePath == "" {
		return "", fmt.Errorf("getFile rejected: %s", result.Description)
	}
	return result.Result.FilePath, nil
}
