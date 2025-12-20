package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
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
	Text       string `json:"text"`
	SetName    string `json:"set_name"`
	Emoji      string `json:"emoji"`
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

	var stickers []*repository.Sticker
	if query == "" {
		stickers, err = s.repo.GetUserStickers(userID, 50, 0)
	} else {
		stickers, err = s.repo.SearchByText(userID, query)
	}

	if err != nil {
		logger.Log.Errorf("Failed to search stickers: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]StickerResponse, 0, len(stickers))
	for _, st := range stickers {
		response = append(response, StickerResponse{
			StickerID:  st.StickerID,
			FileID:     st.FileID,
			DocumentID: st.DocumentID,
			Text:       st.Text,
			SetName:    st.SetName,
			Emoji:      st.Emoji,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
