package semantic

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/ai"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

const (
	describeBatchSize = 10
	embedBatchSize    = 50
	maxResults        = 50
	defaultMinScore   = 0.60
	defaultIndexWait  = 2 * time.Second
	idleWait          = time.Minute
)

// Service indexes media in two independent stages. The vision stage turns an
// image into text and is the expensive one; the embedding stage turns that text
// into a vector and costs almost nothing. Each stage commits per item, so an
// interrupted run never repeats a vision call it already paid for.
type Service struct {
	repo      repository.Repository
	embedder  ai.Embedder
	describer ai.Describer
	minScore  float64
	indexWait time.Duration
}

func New(
	repo repository.Repository,
	embedder ai.Embedder,
	describer ai.Describer,
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
		repo:      repo,
		embedder:  embedder,
		describer: describer,
		minScore:  minScore,
		indexWait: indexWait,
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
	if len(ranked) > maxResults {
		ranked = ranked[:maxResults]
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
	logger.Log.Infow("[AI_INDEX] worker started",
		"embed_model", s.embedder.Model(), "vision_model", s.visionModel())

	for {
		described := s.runDescribeBatch(ctx)
		if ctx.Err() != nil {
			return
		}
		embedded := s.runEmbedBatch(ctx)
		if ctx.Err() != nil {
			return
		}
		if described == 0 && embedded == 0 {
			if !wait(ctx, idleWait) {
				return
			}
		}
	}
}

func (s *Service) visionModel() string {
	if s.describer == nil {
		return ""
	}
	return s.describer.VisionModel()
}

// runDescribeBatch is the paid stage. Every success is written before the next
// item starts, so killing the process loses at most one in-flight call.
func (s *Service) runDescribeBatch(ctx context.Context) int {
	if s.describer == nil {
		return 0
	}
	candidates, err := s.repo.GetAITextCandidates(s.describer.VisionModel(), describeBatchSize)
	if err != nil {
		logger.Log.Errorw("[AI_INDEX] failed to load vision candidates", "error", err)
		wait(ctx, idleWait)
		return 0
	}

	done := 0
	for _, sticker := range candidates {
		if ctx.Err() != nil {
			return done
		}
		// Mark first: a media that keeps failing moves to the back of the queue
		// instead of blocking everything behind it.
		if err := s.repo.MarkAITextAttempt(sticker.UserID, sticker.StickerID); err != nil {
			logger.Log.Warnw("[AI_INDEX] failed to mark vision attempt", "media", sticker.StickerID, "error", err)
		}
		if err := s.describeOne(ctx, sticker); err != nil {
			logger.Log.Warnw("[AI_INDEX] vision failed", "media", sticker.StickerID, "error", err)
		} else {
			done++
			logger.Log.Infow("[AI_INDEX] media described", "media", sticker.StickerID, "type", sticker.MediaType)
		}
		if !wait(ctx, s.indexWait) {
			return done
		}
	}
	return done
}

func (s *Service) describeOne(ctx context.Context, sticker *repository.Sticker) error {
	thumbnail, err := s.repo.GetThumbnail(sticker.FileID)
	if err != nil {
		return fmt.Errorf("thumbnail unavailable: %w", err)
	}
	description, err := s.describer.Describe(ctx, thumbnail, "image/png")
	if err != nil {
		return err
	}
	// Keep the OCR text alongside: it is independent evidence and costs nothing.
	combined := strings.TrimSpace(strings.TrimSpace(sticker.Text) + "\n" + description)
	return s.repo.SaveAIText(
		sticker.UserID, sticker.StickerID,
		s.describer.VisionModel(), combined, sticker.FileID,
	)
}

// runEmbedBatch is the cheap stage and reads only what the vision stage already
// committed, so it can be re-run at any time without extra cost.
func (s *Service) runEmbedBatch(ctx context.Context) int {
	candidates, err := s.repo.GetEmbeddingCandidates(s.embedder.Model(), embedBatchSize)
	if err != nil {
		logger.Log.Errorw("[AI_INDEX] failed to load embedding candidates", "error", err)
		wait(ctx, idleWait)
		return 0
	}

	done := 0
	for _, sticker := range candidates {
		if ctx.Err() != nil {
			return done
		}
		if err := s.repo.MarkEmbeddingAttempt(sticker.UserID, sticker.StickerID); err != nil {
			logger.Log.Warnw("[AI_INDEX] failed to mark embedding attempt", "media", sticker.StickerID, "error", err)
		}
		vector, err := s.embedder.EmbedText(ctx, sticker.AIText)
		if err != nil {
			logger.Log.Warnw("[AI_INDEX] embedding failed", "media", sticker.StickerID, "error", err)
			continue
		}
		if err := s.repo.SaveEmbedding(
			sticker.UserID, sticker.StickerID, s.embedder.Model(),
			sticker.AIText, sticker.FileID, encodeVector(vector),
		); err != nil {
			logger.Log.Warnw("[AI_INDEX] failed to save embedding", "media", sticker.StickerID, "error", err)
			continue
		}
		done++
	}
	if done > 0 {
		logger.Log.Infow("[AI_INDEX] embedded batch", "count", done)
	}
	return done
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
