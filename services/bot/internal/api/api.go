package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	repo   repository.Repository
	apiKey string
	port   int
}

type StickerResponse struct {
	StickerID  string `json:"sticker_id"`
	FileID     string `json:"file_id"`
	DocumentID int64  `json:"document_id"`
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

func New(cfg config.APIConfig, repo repository.Repository) *Server {
	return &Server{
		repo:   repo,
		apiKey: cfg.APIKey,
		port:   cfg.Port,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stickers", s.authMiddleware(s.handleStickers))
	mux.HandleFunc("/api/stickers/", s.authMiddleware(s.handleStickerByID))
	mux.HandleFunc("/api/thumbnails/", s.authMiddleware(s.handleThumbnails))
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
		MediaType:  string(st.MediaType),
		Text:       st.Text,
		SetName:    st.SetName,
		Emoji:      st.Emoji,
		OCREngine:  st.OCREngine,
		ManualEdit: st.ManualEdit,
	}
}
