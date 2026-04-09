package constants

import "time"

const (
	PerPage                = 5
	SearchResultLimit      = 10
	Workers                = 20
	ProgressBarLength      = 10
	MinSearchLength        = 2
	ProgressUpdateInterval = 3

	// Timeouts
	HTTPTimeout     = 30 * time.Second
	StateTTL        = 30 * time.Minute
	InlineCacheTime = 300 // 5 minutes in seconds
)

type OCREngine struct {
	Name  string
	Label string
	Desc  string
}

const OCRSpaceEngineName = "api"

var DefaultOCREngine = OCREngine{
	Name:  OCRSpaceEngineName,
	Label: "OCR.space",
	Desc:  "☁️ Облачный API. Остальные движки отключены.",
}

func GetEngineLabel(name string) string {
	switch name {
	case "", OCRSpaceEngineName:
		return DefaultOCREngine.Label
	default:
		return name
	}
}

func GetEngineDesc(name string) string {
	switch name {
	case "", OCRSpaceEngineName:
		return DefaultOCREngine.Desc
	default:
		return ""
	}
}
