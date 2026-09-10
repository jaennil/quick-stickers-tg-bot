package bot

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestIsVideoDocument(t *testing.T) {
	tests := []struct {
		name     string
		document *models.Document
		want     bool
	}{
		{name: "nil", want: false},
		{name: "video mime", document: &models.Document{MimeType: "video/mp4"}, want: true},
		{name: "uppercase extension", document: &models.Document{FileName: "clip.MP4"}, want: true},
		{name: "webm extension", document: &models.Document{FileName: "clip.webm"}, want: true},
		{name: "image", document: &models.Document{MimeType: "image/png", FileName: "image.png"}, want: false},
		{name: "unrelated document", document: &models.Document{MimeType: "application/pdf", FileName: "file.pdf"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVideoDocument(tt.document); got != tt.want {
				t.Fatalf("isVideoDocument(%#v) = %v, want %v", tt.document, got, tt.want)
			}
		})
	}
}
