// Package animation renders looping previews for media that moves - video
// stickers, GIFs and videos - so the desktop client can play them in the grid.
package animation

import (
	"context"
	"path/filepath"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
)

const (
	batchSize = 10
	// A file that keeps failing (too big for the bot API, corrupt, an
	// unsupported codec) is retried a few times and then left alone.
	maxAttempts = 5
	// The bot API refuses to serve files over 20MB anyway.
	maxSourceBytes = 20 << 20
	itemWait       = time.Second
	idleWait       = time.Minute
)

// Downloader fetches a Telegram file and the path it is stored under.
type Downloader interface {
	Download(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, error)
}

// RenderFunc turns source bytes into a preview; extension picks the decoder.
type RenderFunc func(ctx context.Context, source []byte, extension string) ([]byte, error)

type Service struct {
	repo     repository.Repository
	download Downloader
	render   RenderFunc
	itemWait time.Duration
	idleWait time.Duration
}

func New(repo repository.Repository, download Downloader, render RenderFunc) *Service {
	return &Service{
		repo:     repo,
		download: download,
		render:   render,
		itemWait: itemWait,
		idleWait: idleWait,
	}
}

// Run works through files without a preview until ctx ends. Every preview is
// written as soon as it is rendered, so a restart resumes where it stopped and
// never redoes finished files.
func (s *Service) Run(ctx context.Context) {
	logger.Log.Info("[ANIMATION] worker started")
	for {
		if s.runBatch(ctx) == 0 && !wait(ctx, s.idleWait) {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (s *Service) runBatch(ctx context.Context) int {
	jobs, err := s.repo.GetAnimationCandidates(maxAttempts, batchSize)
	if err != nil {
		logger.Log.Errorw("[ANIMATION] failed to load candidates", "error", err)
		return 0
	}

	done := 0
	for _, job := range jobs {
		if ctx.Err() != nil {
			return done
		}
		// Count the attempt before working, so a file that crashes the render
		// still moves to the back of the queue instead of blocking it.
		if err := s.repo.MarkAnimationAttempt(job.FileID); err != nil {
			logger.Log.Warnw("[ANIMATION] failed to mark attempt", "file", job.FileID, "error", err)
		}
		if err := s.animateOne(ctx, job); err != nil {
			logger.Log.Warnw("[ANIMATION] render failed", "file", job.FileID, "type", job.MediaType, "error", err)
		} else {
			done++
			logger.Log.Infow("[ANIMATION] preview saved", "file", job.FileID, "type", job.MediaType)
		}
		if !wait(ctx, s.itemWait) {
			return done
		}
	}
	return done
}

func (s *Service) animateOne(ctx context.Context, job *repository.AnimationJob) error {
	source, filePath, err := s.download.Download(ctx, job.FileID, maxSourceBytes)
	if err != nil {
		return err
	}
	preview, err := s.render(ctx, source, filepath.Ext(filePath))
	if err != nil {
		return err
	}
	return s.repo.SaveAnimation(job.FileID, preview)
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
