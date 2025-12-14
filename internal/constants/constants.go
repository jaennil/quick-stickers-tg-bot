package constants

const (
	PerPage           = 5
	SearchResultLimit = 10
	Workers           = 5
	ProgressBarLength = 10
	MinSearchLength   = 2
	ProgressUpdateInterval = 3
)

type OCREngine struct {
	Name  string
	Label string
}

var OCREngines = []OCREngine{
	{Name: "api", Label: "OCR.space"},
	{Name: "paddle", Label: "PaddleOCR"},
	{Name: "easy", Label: "EasyOCR"},
	{Name: "tesseract", Label: "Tesseract"},
}

func GetEngineLabel(name string) string {
	for _, e := range OCREngines {
		if e.Name == name {
			return e.Label
		}
	}
	return name
}

func IsValidEngine(name string) bool {
	for _, e := range OCREngines {
		if e.Name == name {
			return true
		}
	}
	return false
}
