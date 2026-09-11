package semantic

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/ai"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"golang.org/x/net/proxy"
)

const (
	indexBatchSize   = 10
	maxMediaSize     = 25 << 20
	defaultMinScore  = 0.25
	defaultIndexWait = 2 * time.Second
)

type Service struct {
	repo          repository.Repository
	embedder      ai.Embedder
	telegramToken string
	mediaClient   *http.Client
	minScore      float64
	indexWait     time.Duration
}

func New(
	repo repository.Repository,
	embedder ai.Embedder,
	telegramToken string,
	proxyURL string,
	minScore float64,
	indexWait time.Duration,
) *Service {
	if minScore == 0 {
		minScore = defaultMinScore
	}
	if indexWait == 0 {
		indexWait = defaultIndexWait
	}
	return &Service{
		repo:          repo,
		embedder:      embedder,
		telegramToken: telegramToken,
		mediaClient:   mediaHTTPClient(proxyURL),
		minScore:      minScore,
		indexWait:     indexWait,
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.embedder != nil
}

func (s *Service) Search(ctx context.Context, userID int64, query string) ([]*repository.Sticker, error) {
	exact, err := s.repo.SearchByText(userID, query)
	if err != nil || !s.Enabled() {
		return exact, err
	}

	queryVector, err := s.embedder.EmbedText(ctx, query)
	if err != nil {
		logger.Log.Warnw("[AI_SEARCH] query embedding failed; using text search", "error", err)
		return exact, nil
	}
	embedded, err := s.repo.GetUserEmbeddings(userID, s.embedder.Model())
	if err != nil {
		return nil, err
	}

	type scoredSticker struct {
		sticker *repository.Sticker
		score   float64
	}
	scores := make(map[string]scoredSticker, len(embedded)+len(exact))
	for _, item := range embedded {
		vector, err := decodeVector(item.Embedding)
		if err != nil || len(vector) != len(queryVector) {
			continue
		}
		score := cosine(queryVector, vector)
		if score >= s.minScore {
			scores[item.Sticker.StickerID] = scoredSticker{sticker: item.Sticker, score: score}
		}
	}
	for index, sticker := range exact {
		score := 2.0 - float64(index)*0.001
		if existing, ok := scores[sticker.StickerID]; ok {
			score += existing.score
		}
		scores[sticker.StickerID] = scoredSticker{sticker: sticker, score: score}
	}

	ranked := make([]scoredSticker, 0, len(scores))
	for _, item := range scores {
		ranked = append(ranked, item)
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > 50 {
		ranked = ranked[:50]
	}
	result := make([]*repository.Sticker, len(ranked))
	for index, item := range ranked {
		result[index] = item.sticker
	}
	return result, nil
}

func (s *Service) RunIndexer(ctx context.Context) {
	if !s.Enabled() {
		logger.Log.Info("[AI_INDEX] disabled")
		return
	}
	logger.Log.Infow("[AI_INDEX] worker started", "model", s.embedder.Model())

	for {
		candidates, err := s.repo.GetEmbeddingCandidates(s.embedder.Model(), indexBatchSize)
		if err != nil {
			logger.Log.Errorw("[AI_INDEX] failed to load candidates", "error", err)
			if !wait(ctx, time.Minute) {
				return
			}
			continue
		}
		if len(candidates) == 0 {
			if !wait(ctx, time.Minute) {
				return
			}
			continue
		}

		for _, sticker := range candidates {
			if err := s.repo.MarkEmbeddingAttempt(sticker.UserID, sticker.StickerID); err != nil {
				logger.Log.Warnw("[AI_INDEX] failed to mark attempt", "media", sticker.StickerID, "error", err)
			}
			if err := s.indexOne(ctx, sticker); err != nil {
				logger.Log.Warnw("[AI_INDEX] media indexing failed", "media", sticker.StickerID, "error", err)
			} else {
				logger.Log.Infow("[AI_INDEX] media indexed", "media", sticker.StickerID, "type", sticker.MediaType)
			}
			if !wait(ctx, s.indexWait) {
				return
			}
		}
	}
}

func (s *Service) indexOne(ctx context.Context, sticker *repository.Sticker) error {
	media, mimeType, video, err := s.embeddingMedia(ctx, sticker)
	if err != nil {
		return err
	}
	vector, err := s.embedder.EmbedMedia(ctx, sticker.Text, media, mimeType, video)
	if err != nil && video {
		thumbnail, thumbnailErr := s.repo.GetThumbnail(sticker.FileID)
		if thumbnailErr == nil {
			vector, err = s.embedder.EmbedMedia(ctx, sticker.Text, thumbnail, "image/png", false)
		}
	}
	if err != nil {
		return err
	}
	return s.repo.SaveEmbedding(
		sticker.UserID,
		sticker.StickerID,
		s.embedder.Model(),
		sticker.Text,
		sticker.FileID,
		encodeVector(vector),
	)
}

func (s *Service) embeddingMedia(ctx context.Context, sticker *repository.Sticker) ([]byte, string, bool, error) {
	if sticker.MediaType == repository.MediaTypeVideo || sticker.MediaType == repository.MediaTypeVideoFile ||
		(sticker.MediaType == repository.MediaTypeGIF && sticker.IsVideo) {
		media, mimeType, err := s.downloadTelegramMedia(ctx, sticker.FileID)
		if err == nil && (mimeType == "video/mp4" || mimeType == "video/quicktime") {
			return media, mimeType, true, nil
		}
	}
	thumbnail, err := s.repo.GetThumbnail(sticker.FileID)
	if err != nil {
		return nil, "", false, fmt.Errorf("thumbnail unavailable: %w", err)
	}
	return thumbnail, "image/png", false, nil
}

func (s *Service) downloadTelegramMedia(ctx context.Context, fileID string) ([]byte, string, error) {
	if s.telegramToken == "" {
		return nil, "", fmt.Errorf("telegram token is empty")
	}
	lookupURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getFile?file_id=%s",
		s.telegramToken,
		url.QueryEscape(fileID),
	)
	response, err := s.mediaClient.Get(lookupURL)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	var fileResponse struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&fileResponse) != nil || !fileResponse.OK {
		return nil, "", fmt.Errorf("telegram getFile returned %s", response.Status)
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", s.telegramToken, fileResponse.Result.FilePath)
	download, err := s.mediaClient.Get(downloadURL)
	if err != nil {
		return nil, "", err
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("telegram download returned %s", download.Status)
	}
	media, err := io.ReadAll(io.LimitReader(download.Body, maxMediaSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(media) > maxMediaSize {
		return nil, "", fmt.Errorf("media exceeds %d bytes", maxMediaSize)
	}
	mimeType := strings.Split(download.Header.Get("Content-Type"), ";")[0]
	if mimeType == "" || mimeType == "application/octet-stream" {
		switch strings.ToLower(strings.TrimPrefix(filepathExtension(fileResponse.Result.FilePath), ".")) {
		case "mov":
			mimeType = "video/quicktime"
		default:
			mimeType = "video/mp4"
		}
	}
	return media, mimeType, nil
}

func filepathExtension(path string) string {
	index := strings.LastIndex(path, ".")
	if index < 0 {
		return ""
	}
	return path[index:]
}

func mediaHTTPClient(proxyURL string) *http.Client {
	client := &http.Client{Timeout: 3 * time.Minute}
	parsedURL, err := url.Parse(proxyURL)
	if proxyURL == "" || err != nil {
		return client
	}
	if parsedURL.Scheme == "socks5" {
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, nil, proxy.Direct)
		if err == nil {
			client.Transport = &http.Transport{
				DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
					return dialer.Dial(network, address)
				},
			}
		}
	} else {
		client.Transport = &http.Transport{Proxy: http.ProxyURL(parsedURL)}
	}
	return client
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(data[index*4:], math.Float32bits(value))
	}
	return data
}

func decodeVector(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding byte length: %d", len(data))
	}
	vector := make([]float32, len(data)/4)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[index*4:]))
	}
	return vector, nil
}

func cosine(left, right []float32) float64 {
	var result float64
	for index := range left {
		result += float64(left[index]) * float64(right[index])
	}
	return result
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
