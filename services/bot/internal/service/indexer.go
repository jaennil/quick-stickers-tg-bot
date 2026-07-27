package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jaennil/sticker-search-bot/internal/constants"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/metrics"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/telegram/fileid"
)

type IndexProgress struct {
	Current       int64
	Total         int
	Processed     int64
	WithText      int64
	Cancelled     bool
	QuotaExceeded bool
	Report        *IndexReport
}

type IndexReport struct {
	Total          int // всего в паке
	ToProcess      int // нужно обработать
	AlreadyIndexed int // уже есть текст
	SkippedManual  int // пропущено (manual_edit)
	NewStickers    int // новые стикеры
	WithText       int // с текстом
	APIUnavailable bool
}

type thumbJob struct {
	FileID          string
	FileURL         string
	StickerType     StickerType
	ThumbnailFileID string // Telegram's built-in thumbnail file_id (for animated/video)
}

type stickerJob struct {
	Sticker models.Sticker
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
	onProgress ProgressCallback,
) IndexProgress {
	total := len(stickerSet.Stickers)
	setName := stickerSet.Name

	report := &IndexReport{Total: total}

	// Get existing stickers for this pack
	existingStickers, err := i.repo.GetStickersBySetName(userID, setName)
	if err != nil {
		logger.Log.Errorw("[INDEX] failed to get existing stickers", "error", err)
		existingStickers = make(map[string]*repository.Sticker)
	}

	// Filter stickers to process
	var stickersToProcess []stickerJob
	for _, sticker := range stickerSet.Stickers {
		existing, exists := existingStickers[sticker.FileUniqueID]
		if exists {
			if existing.ManualEdit {
				report.SkippedManual++
				continue
			}
			if existing.Text != "" {
				report.AlreadyIndexed++
				continue
			}
			stickersToProcess = append(stickersToProcess, stickerJob{Sticker: sticker})
		} else {
			stickersToProcess = append(stickersToProcess, stickerJob{Sticker: sticker})
			report.NewStickers++
		}
	}

	report.ToProcess = len(stickersToProcess)

	logger.Log.Infow("[INDEX] starting pack indexing",
		"pack", setName,
		"total", total,
		"to_process", report.ToProcess,
		"already_indexed", report.AlreadyIndexed,
		"skipped_manual", report.SkippedManual,
		"new", report.NewStickers,
		"workers", constants.Workers,
		"user", userID,
	)

	// If nothing to process, return early
	if len(stickersToProcess) == 0 {
		return IndexProgress{
			Total:  total,
			Report: report,
		}
	}

	var processed atomic.Int64
	var withText atomic.Int64
	var completed atomic.Int64
	var cancelled atomic.Bool
	var quotaExceeded atomic.Bool

	// Create cancellable context for quota exceeded
	indexCtx, cancelIndex := context.WithCancel(ctx)
	defer cancelIndex()

	jobs := make(chan stickerJob, constants.Workers)
	thumbJobs := make(chan thumbJob, 1000) // Large buffer to avoid blocking OCR workers
	var wg sync.WaitGroup
	var thumbWg sync.WaitGroup

	// Start thumbnail workers
	for tw := 0; tw < constants.Workers; tw++ {
		thumbWg.Add(1)
		go func(workerID int) {
			defer thumbWg.Done()
			for job := range thumbJobs {
				select {
				case <-indexCtx.Done():
					return
				default:
				}
				if err := i.DownloadAndSaveThumbnailWithType(indexCtx, job.FileID, job.FileURL, job.StickerType); err != nil {
					logger.Log.Debugw("[THUMB] failed to save thumbnail",
						"worker", workerID,
						"file_id", job.FileID[:20]+"...",
						"error", err,
					)
				} else {
					logger.Log.Debugw("[THUMB] thumbnail saved",
						"worker", workerID,
						"file_id", job.FileID[:20]+"...",
					)
				}
			}
		}(tw)
	}

	// Start OCR workers with staggered delay
	for w := 0; w < constants.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			time.Sleep(time.Duration(workerID*50) * time.Millisecond)
			logger.Log.Debugw("[INDEX] worker started", "worker", workerID)

			for job := range jobs {
				stickerStart := time.Now()
				sticker := job.Sticker
				select {
				case <-indexCtx.Done():
					cancelled.Store(true)
					logger.Log.Warnw("[INDEX] worker cancelled", "worker", workerID)
					return
				default:
				}

				getFileStart := time.Now()
				file, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.FileID})
				metrics.TelegramGetFileDuration.Observe(time.Since(getFileStart).Seconds())
				if err != nil {
					logger.Log.Errorw("[INDEX] failed to get file",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
					metrics.StickersProcessedTotal.WithLabelValues("error").Inc()
					completed.Add(1)
					continue
				}

				fileURL := tgBot.FileDownloadLink(file)

				// Determine sticker type
				var stickerType StickerType
				if sticker.IsVideo {
					stickerType = StickerTypeVideo
					logger.Log.Debugw("[INDEX] processing video sticker",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
					)
				} else if sticker.IsAnimated {
					stickerType = StickerTypeAnimated
					logger.Log.Debugw("[INDEX] processing animated sticker",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
					)
				} else {
					stickerType = StickerTypeStatic
				}

				text, ocrErr := i.DownloadAndOCRWithType(indexCtx, fileURL, stickerType)

				// Check for quota exceeded
				if errors.Is(ocrErr, ocr.ErrQuotaExceeded) {
					logger.Log.Warnw("[INDEX] quota exceeded, stopping",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
					)
					quotaExceeded.Store(true)
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

				// Skip stickers without text - they won't be searchable anyway
				if text == "" {
					logger.Log.Debugw("[INDEX] skipping sticker (no text)",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"emoji", sticker.Emoji,
					)
					metrics.StickersProcessedTotal.WithLabelValues("no_text").Inc()
					completed.Add(1)
					continue
				}

				// Decode document_id from file_id
				documentID, _ := fileid.DecodeDocumentID(sticker.FileID)

				s := &repository.Sticker{
					UserID:     userID,
					StickerID:  sticker.FileUniqueID,
					SetName:    setName,
					FileID:     sticker.FileID,
					DocumentID: documentID,
					Text:       text,
					Emoji:      sticker.Emoji,
					OCREngine:  constants.OCRSpaceEngineName,
					IsAnimated: sticker.IsAnimated,
					IsVideo:    sticker.IsVideo,
				}

				dbStart := time.Now()
				if err := i.repo.SaveSticker(s); err != nil {
					metrics.DBSaveDuration.Observe(time.Since(dbStart).Seconds())
					logger.Log.Errorw("[INDEX] failed to save sticker",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"error", err,
					)
					metrics.StickersProcessedTotal.WithLabelValues("error").Inc()
				} else {
					metrics.DBSaveDuration.Observe(time.Since(dbStart).Seconds())
					processed.Add(1)
					withText.Add(1)
					// Record sticker type for metrics
					var typeLabel string
					switch {
					case sticker.IsVideo:
						typeLabel = "video"
					case sticker.IsAnimated:
						typeLabel = "animated"
					default:
						typeLabel = "static"
					}
					metrics.StickerProcessingDuration.WithLabelValues(typeLabel).Observe(time.Since(stickerStart).Seconds())
					metrics.StickersProcessedTotal.WithLabelValues("success").Inc()
					// Queue thumbnail download
					tj := thumbJob{FileID: sticker.FileID, FileURL: fileURL, StickerType: stickerType}

					// For animated/video stickers, use Telegram's built-in thumbnail
					if (sticker.IsAnimated || sticker.IsVideo) && sticker.Thumbnail != nil {
						thumbFile, err := tgBot.GetFile(indexCtx, &bot.GetFileParams{FileID: sticker.Thumbnail.FileID})
						if err == nil {
							tj.ThumbnailFileID = sticker.Thumbnail.FileID
							tj.FileURL = tgBot.FileDownloadLink(thumbFile)
							tj.StickerType = StickerTypeStatic // Telegram thumbnail is already a static image
						}
					}

					thumbJobs <- tj
					logger.Log.Infow("[INDEX] sticker saved",
						"worker", workerID,
						"sticker", sticker.FileUniqueID,
						"text", text,
						"emoji", sticker.Emoji,
					)
				}
				completed.Add(1)
			}
		}(w)
	}

	// Send jobs in goroutine
	go func() {
		for _, job := range stickersToProcess {
			select {
			case <-indexCtx.Done():
				close(jobs)
				return
			case jobs <- job:
			}
		}
		close(jobs)
	}()

	// Progress updates
	go func() {
		lastUpdate := int64(0)
		toProcess := int64(len(stickersToProcess))
		for {
			select {
			case <-indexCtx.Done():
				return
			default:
			}

			current := completed.Load()
			if current >= toProcess {
				break
			}
			if current-lastUpdate >= constants.ProgressUpdateInterval {
				if onProgress != nil {
					onProgress(IndexProgress{
						Current:       current,
						Total:         int(toProcess),
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
	close(thumbJobs)
	thumbWg.Wait()

	report.WithText = int(withText.Load())
	report.APIUnavailable = quotaExceeded.Load()

	result := IndexProgress{
		Current:       completed.Load(),
		Total:         len(stickersToProcess),
		Processed:     processed.Load(),
		WithText:      withText.Load(),
		Cancelled:     cancelled.Load(),
		QuotaExceeded: quotaExceeded.Load(),
		Report:        report,
	}

	logger.Log.Infow("[INDEX] pack indexing completed",
		"pack", setName,
		"total", total,
		"to_process", report.ToProcess,
		"processed", result.Processed,
		"with_text", result.WithText,
		"already_indexed", report.AlreadyIndexed,
		"skipped_manual", report.SkippedManual,
		"cancelled", result.Cancelled,
		"quota_exceeded", result.QuotaExceeded,
		"user", userID,
	)

	return result
}

// StickerType represents the type of sticker
type StickerType int

const (
	StickerTypeStatic   StickerType = iota // WebP static
	StickerTypeAnimated                    // TGS (Lottie)
	StickerTypeVideo                       // WEBM video
)

func (i *Indexer) DownloadAndOCR(ctx context.Context, fileURL string) (string, error) {
	return i.DownloadAndOCRWithType(ctx, fileURL, StickerTypeStatic)
}

func (i *Indexer) DownloadAndOCRWithType(ctx context.Context, fileURL string, stickerType StickerType) (string, error) {
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

	// Determine file extension based on sticker type
	var ext string
	switch stickerType {
	case StickerTypeAnimated:
		ext = ".tgs"
	case StickerTypeVideo:
		ext = ".webm"
	default:
		ext = ".webp"
	}

	tmpFile, err := os.CreateTemp("", "sticker-*"+ext)
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

	// Convert based on sticker type
	switch stickerType {
	case StickerTypeAnimated:
		// TGS (Lottie) - extract first frame using lottie2gif then convert
		if err := extractTGSFrame(ctx, tmpFile.Name(), pngPath); err != nil {
			logger.Log.Warnw("[OCR] TGS frame extraction failed", "error", err)
			return "", err
		}
	case StickerTypeVideo:
		return i.recognizeVideoFrames(ctx, tmpFile.Name(), pngPath)
	default:
		// Static WebP - convert to PNG
		if err := convertWebPToPNG(ctx, tmpFile.Name(), pngPath); err != nil {
			return i.ocr.RecognizeText(ctx, tmpFile.Name())
		}
	}

	return i.ocr.RecognizeText(ctx, pngPath)
}

// recognizeVideoFrames checks the first frame and then one frame per second
// until OCR finds text or the video ends.
func (i *Indexer) recognizeVideoFrames(ctx context.Context, videoPath, pngPath string) (string, error) {
	duration, err := videoDuration(ctx, videoPath)
	if err != nil {
		logger.Log.Warnw("[OCR] failed to read video duration; checking first frame only", "error", err)
		duration = 1
	}

	samples := max(1, int(math.Ceil(duration)))
	for second := range samples {
		if err := extractVideoFrameAt(ctx, videoPath, pngPath, float64(second)); err != nil {
			return "", err
		}

		text, err := i.ocr.RecognizeText(ctx, pngPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" {
			logger.Log.Debugw("[OCR] found text in video", "second", second)
			return text, nil
		}
	}

	return "", nil
}

func videoDuration(ctx context.Context, videoPath string) (float64, error) {
	output, err := exec.CommandContext(ctx,
		"ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", videoPath,
	).Output()
	if err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid video duration %q", strings.TrimSpace(string(output)))
	}
	return duration, nil
}

// extractTGSFrame extracts the first frame from a TGS (Lottie) file
func extractTGSFrame(ctx context.Context, tgsPath, pngPath string) error {
	// TGS is gzipped Lottie JSON
	// Use lottie_convert.py from python-lottie or tgs2png
	// For now, use tgs2png if available, otherwise try python

	// Try tgs2png first (faster, if available)
	cmd := exec.CommandContext(ctx, "tgs2png", tgsPath, "-o", pngPath, "-f", "0")
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Fallback: use python with lottie
	gifPath := strings.TrimSuffix(pngPath, ".png") + ".gif"
	defer os.Remove(gifPath)

	// Convert TGS to GIF using lottie
	cmd = exec.CommandContext(ctx, "python3", "-c", `
import gzip
import json
import sys
from lottie.importers.tgs import import_tgs
from lottie.exporters.gif import export_gif

with gzip.open(sys.argv[1], 'rb') as f:
    animation = import_tgs(f)

# Export only first frame
animation.out_point = animation.in_point + 1
export_gif(animation, sys.argv[2], skip_frames=0)
`, tgsPath, gifPath)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Convert GIF to PNG
	return exec.CommandContext(ctx, "convert", gifPath+"[0]", pngPath).Run()
}

// extractVideoFrame extracts the first frame from a video.
func extractVideoFrame(ctx context.Context, videoPath, pngPath string) error {
	return extractVideoFrameAt(ctx, videoPath, pngPath, 0)
}

func extractVideoFrameAt(ctx context.Context, videoPath, pngPath string, second float64) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", strconv.FormatFloat(second, 'f', 3, 64), "-i", videoPath, "-vframes", "1", "-f", "image2", pngPath)
	return cmd.Run()
}

func convertWebPToPNG(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "convert", src, dst)
	return cmd.Run()
}

func (i *Indexer) DownloadAndSaveThumbnail(ctx context.Context, fileID, fileURL string) error {
	return i.DownloadAndSaveThumbnailWithType(ctx, fileID, fileURL, StickerTypeStatic)
}

func (i *Indexer) DownloadAndSaveThumbnailWithType(ctx context.Context, fileID, fileURL string, stickerType StickerType) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Determine file extension based on sticker type
	var ext string
	switch stickerType {
	case StickerTypeAnimated:
		ext = ".tgs"
	case StickerTypeVideo:
		ext = ".webm"
	default:
		ext = ".webp"
	}

	tmpFile, err := os.CreateTemp("", "thumb-*"+ext)
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return err
	}
	tmpFile.Close()

	// Convert to PNG
	pngPath := strings.TrimSuffix(tmpFile.Name(), filepath.Ext(tmpFile.Name())) + ".png"
	defer os.Remove(pngPath)

	// Convert based on sticker type
	switch stickerType {
	case StickerTypeAnimated:
		if err := extractTGSFrame(ctx, tmpFile.Name(), pngPath); err != nil {
			logger.Log.Warnw("[THUMB] TGS frame extraction failed", "error", err)
			return err
		}
	case StickerTypeVideo:
		if err := extractVideoFrame(ctx, tmpFile.Name(), pngPath); err != nil {
			logger.Log.Warnw("[THUMB] WEBM frame extraction failed", "error", err)
			return err
		}
	default:
		cmd := exec.CommandContext(ctx, "convert", tmpFile.Name(), pngPath)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Read PNG file
	pngData, err := os.ReadFile(pngPath)
	if err != nil {
		return err
	}

	return i.repo.SaveThumbnail(fileID, pngData)
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
