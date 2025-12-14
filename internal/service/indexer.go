package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

type IndexProgress struct {
	Current            int64
	Total              int
	Processed          int64
	WithText           int64
	Cancelled          bool
	QuotaExceeded      bool
	LastStickerFileURL string
	RemainingStickers  []models.Sticker
}

type ProgressCallback func(progress IndexProgress)

type Indexer struct {
	repo repository.Repository
	ocr  *ocr.OCR
}

func NewIndexer(repo repository.Repository, ocr *ocr.OCR) *Indexer {
	return &Indexer{repo: repo, ocr: ocr}
}

func (i *Indexer) IndexPack(
	ctx context.Context,
	tgBot *bot.Bot,
	stickerSet *models.StickerSet,
	userID int64,
	ocrEngine string,
	onProgress ProgressCallback,
) IndexProgress {
	total := len(stickerSet.Stickers)
	setName := stickerSet.Name

	logger.Log.Infow("[INDEX] starting pack indexing",
		"pack", setName,
		"total", total,
		"engine", ocrEngine,
		"workers", constants.Workers,
		"user", userID,
	)

	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool
	var quotaExceeded atomic.Bool
	var lastStickerURL atomic.Value
	var remainingMu sync.Mutex
	var remainingStickers []models.Sticker

	// Create cancellable context for quota exceeded
	indexCtx, cancelIndex := context.WithCancel(ctx)
	defer cancelIndex()

	jobs := make(chan models.Sticker, constants.Workers)
	var wg sync.WaitGroup

	// Start workers with staggered delay
	for w := 0; w < constants.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*50) * time.Millisecond)
			logger.Log.Debugw("[INDEX] worker started", "worker", workerID)

			for sticker := range jobs {
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					logger.Log.Warnw("[INDEX] worker cancelled", "worker", workerID)
					return
				default:
				}

				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				if err != nil {
					logger.Log.Errorw("[INDEX] failed to get file",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)
				text, ocrErr := i.DownloadAndOCR(indexCtx, fileURL, ocrEngine)

				// Check for quota exceeded
				if errors.Is(ocrErr, ocr.ErrQuotaExceeded) {
					logger.Log.Warnw("[INDEX] quota exceeded, pausing",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
					)
					quotaExceeded.Store(true)
					lastStickerURL.Store(fileURL)
					cancelIndex() // Stop all workers
					return
				}

				if ocrErr != nil {
					logger.Log.Warnw("[INDEX] OCR failed",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", ocrErr,
					)
				}

				s := &repository.Sticker{
					UserID:    userID,
					StickerID: sticker.FileUniqueID,
					SetName:   setName,
					FileID:    sticker.FileID,
					Text:      text,
					Emoji:     sticker.Emoji,
				}

				if err := i.repo.SaveSticker(s); err != nil {
					logger.Log.Errorw("[INDEX] failed to save sticker",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
				} else {
					processed.Add(1)
					if text != "" {
						withText.Add(1)
						logger.Log.Infow("[INDEX] sticker processed",
							"worker", workerID,
							"sticker", sticker.FileUniqueID,
							"text", text,
							"emoji", sticker.Emoji,
						)
					} else {
						logger.Log.Debugw("[INDEX] sticker processed (no text)",
							"worker", workerID,
							"sticker", sticker.FileUniqueID,
							"emoji", sticker.Emoji,
						)
					}
				}
				completed.Add(1)
			}
		}(w)
	}

	// Send jobs in goroutine
	go func() {
		for idx, sticker := range stickerSet.Stickers {
			select {
			case <-indexCtx.Done():
				// Collect remaining stickers
				remainingMu.Lock()
				remainingStickers = append(remainingStickers, stickerSet.Stickers[idx:]...)
				remainingMu.Unlock()
				close(jobs)
				return
			case jobs <- sticker:
			}
		}
		close(jobs)
	}()

	// Progress updates
	go func() {
		lastUpdate := int64(0)
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= int64(total) {
				break
			}
			if current-lastUpdate >= constants.ProgressUpdateInterval {
				if onProgress != nil {
					onProgress(IndexProgress{
						Current:       current,
						Total:         total,
						Processed:     processed.Load(),
						WithText:      withText.Load(),
						Cancelled:     cancelled.Load(),
						QuotaExceeded: quotaExceeded.Load(),
					})
				}
				lastUpdate = current
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	wg.Wait()

	// Get last sticker URL if set
	var lastURL string
	if v := lastStickerURL.Load(); v != nil {
		lastURL = v.(string)
	}

	remainingMu.Lock()
	remaining := remainingStickers
	remainingMu.Unlock()

	result := IndexProgress{
		Current:            completed.Load(),
		Total:              total,
		Processed:          processed.Load(),
		WithText:           withText.Load(),
		Cancelled:          cancelled.Load(),
		QuotaExceeded:      quotaExceeded.Load(),
		LastStickerFileURL: lastURL,
		RemainingStickers:  remaining,
	}

	logger.Log.Infow("[INDEX] pack indexing completed",
		"pack", setName,
		"total", total,
		"processed", result.Processed,
		"with_text", result.WithText,
		"cancelled", result.Cancelled,
		"quota_exceeded", result.QuotaExceeded,
		"remaining", len(result.RemainingStickers),
		"user", userID,
	)

	return result
}

func (i *Indexer) DownloadAndOCR(ctx context.Context, fileURL string, engine string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "sticker-*.webp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", err
	}
	tmpFile.Close()

	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err != nil {
		return i.ocr.RecognizeText(ctx, tmpFile.Name(), engine)
	}

	return i.ocr.RecognizeText(ctx, pngPath, engine)
}

func convertWebPToPNG(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "convert", src, dst)
	return cmd.Run()
}

// CompareOCREngines downloads sticker and compares all OCR engines
func (i *Indexer) CompareOCREngines(ctx context.Context, fileURL string) []ocr.CompareResult {
	resp, err := http.Get(fileURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "sticker-compare-*.webp")
	if err != nil {
		return nil
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return nil
	}
	tmpFile.Close()

	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	imagePath := tmpFile.Name()
	if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err == nil {
		imagePath = pngPath
	}

	return i.ocr.CompareEngines(ctx, imagePath)
}

func ProgressBar(current, total int) string {
	if total == 0 {
		return "[░░░░░░░░░░]"
	}

	filled := current * constants.ProgressBarLength / total
	if filled > constants.ProgressBarLength {
		filled = constants.ProgressBarLength
	}

	bar := "["
	for j := 0; j < constants.ProgressBarLength; j++ {
		if j < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	bar += "]"
	return bar
}
