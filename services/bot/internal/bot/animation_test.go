package bot

import "testing"

func TestAnimationUsesVideoContainer(t *testing.T) {
	tests := []struct {
		mimeType string
		fileName string
		want     bool
	}{
		{mimeType: "video/mp4", want: true},
		{fileName: "telegram-animation.MP4", want: true},
		{mimeType: "image/gif", fileName: "animation.gif", want: false},
	}

	for _, tt := range tests {
		if got := animationUsesVideoContainer(tt.mimeType, tt.fileName); got != tt.want {
			t.Fatalf("animationUsesVideoContainer(%q, %q) = %v, want %v", tt.mimeType, tt.fileName, got, tt.want)
		}
	}
}
