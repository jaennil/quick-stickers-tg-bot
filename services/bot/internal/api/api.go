package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/proxy"
)

type Server struct {
	repo          repository.Repository
	apiKey        string
	port          int
	telegramToken string
	httpClient    *http.Client
}

type StickerResponse struct {
	StickerID  string `json:"sticker_id"`
	FileID     string `json:"file_id"`
	DocumentID int64  `json:"document_id"`
	IsAnimated bool   `json:"is_animated"`
	IsVideo    bool   `json:"is_video"`
	MediaType  string `json:"media_type"`
	Text       string `json:"text"`
	SetName    string `json:"set_name"`
	Emoji      string `json:"emoji"`
	OCREngine  string `json:"ocr_engine"`
	ManualEdit bool   `json:"manual_edit"`
}

type UpdateStickerRequest struct {
	UserID int64  `json:"user_id"`
	Text   string `json:"text"`
}

func New(cfg config.APIConfig, repo repository.Repository, telegramToken, proxyURL string) *Server {
	return &Server{
		repo:          repo,
		apiKey:        cfg.APIKey,
		port:          cfg.Port,
		telegramToken: telegramToken,
		httpClient:    newHTTPClient(proxyURL),
	}
}

func newHTTPClient(proxyURL string) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if proxyURL == "" {
		return client
	}
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		logger.Log.Warnw("[API] invalid media proxy URL", "error", err)
		return client
	}
	if parsedURL.Scheme == "socks5" {
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, nil, proxy.Direct)
		if err != nil {
			logger.Log.Warnw("[API] failed to create media SOCKS5 dialer", "error", err)
			return client
		}
		client.Transport = &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		logger.Log.Info("[API] using SOCKS5 proxy for Telegram media")
		return client
	}
	client.Transport = &http.Transport{Proxy: http.ProxyURL(parsedURL)}
	logger.Log.Info("[API] using HTTP proxy for Telegram media")
	return client
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stickers", s.authMiddleware(s.handleStickers))
	mux.HandleFunc("/api/stickers/", s.authMiddleware(s.handleStickerByID))
	mux.HandleFunc("/api/thumbnails/", s.authMiddleware(s.handleThumbnails))
	mux.HandleFunc("/api/media/", s.authMiddleware(s.handleMedia))
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", s.port)
	logger.Log.Infof("Starting API server on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey != "" {
			key := r.Header.Get("X-API-Key")
			if key != s.apiKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleStickers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userIDStr := r.URL.Query().Get("user_id")
	if userIDStr == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}

	query := r.URL.Query().Get("query")

	// Parse limit and offset for pagination
	limit := 50
	offset := 0
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	var stickers []*repository.Sticker
	if query == "" {
		logger.Log.Debugw("fetching user stickers", "user_id", userID, "limit", limit, "offset", offset)
		stickers, err = s.repo.GetUserStickers(userID, limit, offset)
	} else {
		logger.Log.Debugw("searching stickers", "user_id", userID, "query", query)
		stickers, err = s.repo.SearchByText(userID, query)
	}

	if err != nil {
		logger.Log.Errorf("Failed to search stickers: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]StickerResponse, 0, len(stickers))
	for _, st := range stickers {
		response = append(response, stickerResponseFromRepo(st))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleStickerByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stickerID := strings.TrimPrefix(r.URL.Path, "/api/stickers/")
	if stickerID == "" || strings.Contains(stickerID, "/") {
		http.Error(w, "invalid sticker_id", http.StatusBadRequest)
		return
	}

	unescapedStickerID, err := url.PathUnescape(stickerID)
	if err != nil || unescapedStickerID == "" {
		http.Error(w, "invalid sticker_id", http.StatusBadRequest)
		return
	}

	var req UpdateStickerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == 0 {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	if err := s.repo.UpdateStickerText(req.UserID, unescapedStickerID, req.Text); err != nil {
		logger.Log.Errorf("Failed to update sticker text: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	sticker, err := s.repo.GetSticker(req.UserID, unescapedStickerID)
	if err != nil {
		logger.Log.Errorf("Failed to fetch updated sticker: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if sticker == nil {
		http.Error(w, "Sticker not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stickerResponseFromRepo(sticker))
}

func (s *Server) handleThumbnails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract file_id from path: /api/thumbnails/{file_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/thumbnails/")
	if path == "" {
		http.Error(w, "file_id is required", http.StatusBadRequest)
		return
	}

	thumbnail, err := s.repo.GetThumbnail(path)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(thumbnail)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}
	stickerID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/media/"))
	if err != nil || stickerID == "" {
		http.Error(w, "invalid sticker_id", http.StatusBadRequest)
		return
	}
	media, err := s.repo.GetSticker(userID, stickerID)
	if err != nil || media == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if s.telegramToken == "" {
		http.Error(w, "Telegram media unavailable", http.StatusServiceUnavailable)
		return
	}

	getFileURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", s.telegramToken, url.QueryEscape(media.FileID))
	response, err := s.httpClient.Get(getFileURL)
	if err != nil {
		logger.Log.Warnw("[API] failed to resolve Telegram media", "media", stickerID, "error", err)
		http.Error(w, "Failed to resolve media", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	var fileResponse struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&fileResponse) != nil || !fileResponse.OK {
		logger.Log.Warnw("[API] Telegram rejected media lookup", "media", stickerID, "status", response.StatusCode)
		http.Error(w, "Failed to resolve media", http.StatusBadGateway)
		return
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", s.telegramToken, fileResponse.Result.FilePath)
	download, err := s.httpClient.Get(downloadURL)
	if err != nil {
		logger.Log.Warnw("[API] failed to download Telegram media", "media", stickerID, "error", err)
		http.Error(w, "Failed to download media", http.StatusBadGateway)
		return
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		http.Error(w, "Failed to download media", http.StatusBadGateway)
		return
	}

	if contentType := download.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else if media.MediaType == repository.MediaTypeVideo {
		w.Header().Set("Content-Type", "video/mp4")
	}
	w.Header().Set("Content-Disposition", "attachment")
	if contentLength := download.Header.Get("Content-Length"); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, download.Body); err != nil {
		logger.Log.Warnw("[API] failed to stream media", "media", stickerID, "error", err)
	}
}

func stickerResponseFromRepo(st *repository.Sticker) StickerResponse {
	documentID := st.DocumentID
	if documentID == 0 && st.MediaType == repository.MediaTypeSticker {
		if decodedDocumentID, err := fileid.DecodeDocumentID(st.FileID); err == nil {
			documentID = decodedDocumentID
		}
	}

	return StickerResponse{
		StickerID:  st.StickerID,
		FileID:     st.FileID,
		DocumentID: documentID,
		IsAnimated: st.IsAnimated,
		IsVideo:    st.IsVideo,
		MediaType:  string(st.MediaType),
		Text:       st.Text,
		SetName:    st.SetName,
		Emoji:      st.Emoji,
		OCREngine:  st.OCREngine,
		ManualEdit: st.ManualEdit,
	}
}
