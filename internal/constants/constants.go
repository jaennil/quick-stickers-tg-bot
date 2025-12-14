package constants

const (
	PerPage           = 5
	SearchResultLimit = 10
	Workers           = 100
	ProgressBarLength = 10
	MinSearchLength   = 2
	ProgressUpdateInterval = 3
)

type OCREngine struct {
	Name  string
	Label string
	Desc  string
}

var OCREngines = []OCREngine{
	{Name: "api", Label: "OCR.space", Desc: "☁️ Облачный API. Лучшее качество, но 180 запросов/час"},
	{Name: "paddle", Label: "PaddleOCR", Desc: "🔷 Нейросеть от Baidu. Хорошее качество, работает локально"},
	{Name: "easy", Label: "EasyOCR", Desc: "🔶 Нейросеть на PyTorch. Хорошо для разных языков"},
	{Name: "tesseract", Label: "Tesseract", Desc: "📦 Классический OCR от Google. Быстрый, но качество хуже"},
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
