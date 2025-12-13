package ocr

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type OCR struct{}

func New() *OCR {
	return &OCR{}
}

// RecognizeText распознает текст на изображении через Tesseract
func (o *OCR) RecognizeText(imagePath string) (string, error) {
	// tesseract image.png stdout -l rus+eng
	cmd := exec.Command("tesseract", imagePath, "stdout", "-l", "rus+eng")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract error: %w, stderr: %s", err, stderr.String())
	}

	text := strings.TrimSpace(stdout.String())
	return text, nil
}

// IsAvailable проверяет доступен ли Tesseract
func (o *OCR) IsAvailable() bool {
	cmd := exec.Command("tesseract", "--version")
	return cmd.Run() == nil
}
