package animation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// The grid shows previews at most a couple hundred pixels wide, and a
	// sticker loop rarely runs longer than a few seconds, so this keeps files
	// small without visibly costing quality.
	previewSize     = 256
	previewFPS      = 15
	previewSeconds  = 6
	previewQuality  = 70
	maxPreviewBytes = 4 << 20
)

// Render converts a video (webm sticker, mp4 GIF, video) into a looping
// animated WebP the client can decode without ffmpeg of its own.
func Render(ctx context.Context, source []byte, extension string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "animation-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	extension = strings.ToLower(extension)
	if extension == "" {
		extension = ".mp4"
	}
	input := filepath.Join(dir, "input"+extension)
	output := filepath.Join(dir, "preview.webp")
	if err := os.WriteFile(input, source, 0o600); err != nil {
		return nil, err
	}

	// Video stickers carry an alpha channel that only libvpx decodes; the
	// built-in VP9 decoder drops it and the sticker gets a black backdrop.
	// Non-VP9 webm (some user-sent files) cannot use libvpx-vp9, so retry
	// with the default decoder rather than give up on them.
	var renderErr error
	if extension == ".webm" {
		if renderErr = runFFmpeg(ctx, input, output, "libvpx-vp9"); renderErr == nil {
			return readPreview(output)
		}
	}
	if err := runFFmpeg(ctx, input, output, ""); err != nil {
		if renderErr != nil {
			return nil, fmt.Errorf("%v; fallback: %w", renderErr, err)
		}
		return nil, err
	}
	return readPreview(output)
}

func runFFmpeg(ctx context.Context, input, output, decoder string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if decoder != "" {
		args = append(args, "-c:v", decoder)
	}
	args = append(args,
		"-i", input,
		"-t", fmt.Sprint(previewSeconds),
		"-an",
		// eof_action=pass keeps the last frame at end of input. Without it a
		// clip shorter than one output frame (a single-frame "video" sticker
		// lasting 1/30s) comes out of the fps filter empty, and the encoder
		// then fails to assemble an animation with nothing in it.
		"-vf", fmt.Sprintf(
			"fps=%d:eof_action=pass,scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos",
			previewFPS, previewSize, previewSize),
		"-c:v", "libwebp_anim",
		"-quality", fmt.Sprint(previewQuality),
		"-loop", "0",
		output,
	)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func readPreview(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("ffmpeg produced an empty preview")
	}
	if len(data) > maxPreviewBytes {
		return nil, fmt.Errorf("preview is %d bytes, over the %d limit", len(data), maxPreviewBytes)
	}
	return data, nil
}
