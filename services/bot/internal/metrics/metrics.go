package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OCR metrics
	OCRRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ocr_request_duration_seconds",
		Help:    "Duration of OCR requests in seconds",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
	}, []string{"status"}) // status: success, error, quota_exceeded

	OCRRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ocr_requests_total",
		Help: "Total number of OCR requests",
	}, []string{"status"})

	// Telegram API metrics
	TelegramGetFileDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "telegram_getfile_duration_seconds",
		Help:    "Duration of Telegram GetFile API calls",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5},
	})

	TelegramDownloadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "telegram_download_duration_seconds",
		Help:    "Duration of downloading files from Telegram",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10},
	})

	// Sticker processing metrics
	StickerProcessingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sticker_processing_duration_seconds",
		Help:    "Total duration of processing a single sticker",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"type"}) // type: static, animated, video

	StickersProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stickers_processed_total",
		Help: "Total number of stickers processed",
	}, []string{"status"}) // status: success, error, no_text

	// Database metrics
	DBSaveDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "db_save_duration_seconds",
		Help:    "Duration of saving sticker to database",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1},
	})

	// Thumbnail metrics
	ThumbnailSaveDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "thumbnail_save_duration_seconds",
		Help:    "Duration of saving thumbnail",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5},
	})

	// Active workers
	ActiveWorkers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_active_workers",
		Help: "Number of active indexer workers",
	})

	// Queue size
	JobQueueSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_job_queue_size",
		Help: "Number of jobs waiting in queue",
	})
)
