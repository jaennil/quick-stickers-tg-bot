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
	HTTPTimeout      = 30 * time.Second
	StateTTL         = 30 * time.Minute
	InlineCacheTime  = 300 // 5 minutes in seconds
)

type OCREngine struct {
	Name  string
	Label string
	Desc  string
}

var OCREngines = []OCREngine{
	{Name: "api", Label: "OCR.space", Desc: "☁️ Облачный API. Лучшее качество"},
}

func GetEngineLabel(name string) string {
	for _, e := range OCREngines {
		if e.Name == name {
			return e.Label
		}
	}
	return name
}

func GetEngineDesc(name string) string {
	for _, e := range OCREngines {
		if e.Name == name {
			return e.Desc
		}
	}
	return ""
}

func IsValidEngine(name string) bool {
	for _, e := range OCREngines {
		if e.Name == name {
			return true
		}
	}
	return false
}
