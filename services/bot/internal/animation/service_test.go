package animation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite"
)

func TestMain(m *testing.M) {
	logger.Init()
	os.Exit(m.Run())
}

// goose panics if migrations are registered twice in one process, so the
// package shares one database and each test owns distinct file ids.
var (
	sharedRepo *repository.BaseRepository
	repoOnce   sync.Once
	nextID     atomic.Int64
)

func newRepo(t *testing.T) *repository.BaseRepository {
	t.Helper()
	repoOnce.Do(func() {
		dir, err := os.MkdirTemp("", "animation-test")
		if err != nil {
			panic(err)
		}
		sharedRepo, err = sqlite.New(filepath.Join(dir, "anim.db"))
		if err != nil {
			panic(err)
		}
	})
	return sharedRepo
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nextID.Add(1))
}

func save(t *testing.T, repo *repository.BaseRepository, s *repository.Sticker) {
	t.Helper()
	if err := repo.SaveSticker(s); err != nil {
		t.Fatal(err)
	}
}

func candidateIDs(t *testing.T, repo *repository.BaseRepository) map[string]*repository.AnimationJob {
	t.Helper()
	jobs, err := repo.GetAnimationCandidates(maxAttempts, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*repository.AnimationJob{}
	for _, job := range jobs {
		out[job.FileID] = job
	}
	return out
}

func TestCandidatesAreOnlyMediaThatMoves(t *testing.T) {
	repo := newRepo(t)
	videoSticker, gif, video, staticSticker, photo, lottie :=
		uniq("vs"), uniq("gif"), uniq("vid"), uniq("st"), uniq("ph"), uniq("tgs")

	save(t, repo, &repository.Sticker{UserID: 1, StickerID: videoSticker, FileID: videoSticker, IsVideo: true, MediaType: repository.MediaTypeSticker})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: gif, FileID: gif, IsVideo: true, MediaType: repository.MediaTypeGIF})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: video, FileID: video, MediaType: repository.MediaTypeVideo})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: staticSticker, FileID: staticSticker, MediaType: repository.MediaTypeSticker})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: photo, FileID: photo, MediaType: repository.MediaTypePhoto})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: lottie, FileID: lottie, IsAnimated: true, MediaType: repository.MediaTypeSticker})

	got := candidateIDs(t, repo)
	for _, want := range []string{videoSticker, gif, video} {
		if got[want] == nil {
			t.Errorf("%s moves and must be queued", want)
		}
	}
	for _, skip := range []string{staticSticker, photo, lottie} {
		if got[skip] != nil {
			t.Errorf("%s must not be queued", skip)
		}
	}
	if !got[videoSticker].IsVideo || got[gif].MediaType != repository.MediaTypeGIF {
		t.Errorf("job lost its media details: %+v / %+v", got[videoSticker], got[gif])
	}
}

// Two users owning the same file must not cause it to be rendered twice.
func TestSharedFileIsOneJob(t *testing.T) {
	repo := newRepo(t)
	file := uniq("shared")
	save(t, repo, &repository.Sticker{UserID: 10, StickerID: file, FileID: file, IsVideo: true})
	save(t, repo, &repository.Sticker{UserID: 11, StickerID: file, FileID: file, IsVideo: true})

	jobs, err := repo.GetAnimationCandidates(maxAttempts, 1000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range jobs {
		if job.FileID == file {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared file queued %d times", count)
	}
}

func TestSavedPreviewLeavesTheQueue(t *testing.T) {
	repo := newRepo(t)
	file := uniq("done")
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: file, FileID: file, IsVideo: true})
	if err := repo.SaveAnimation(file, []byte("webp")); err != nil {
		t.Fatal(err)
	}
	if candidateIDs(t, repo)[file] != nil {
		t.Fatal("a file with a preview must not be rendered again")
	}
	got, err := repo.GetAnimation(file)
	if err != nil || string(got) != "webp" {
		t.Fatalf("preview not readable back: %q, %v", got, err)
	}
}

func TestFailingFileIsGivenUpOn(t *testing.T) {
	repo := newRepo(t)
	file := uniq("bad")
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: file, FileID: file, IsVideo: true})
	for i := 0; i < maxAttempts; i++ {
		if candidateIDs(t, repo)[file] == nil {
			t.Fatalf("dropped after only %d attempts", i)
		}
		if err := repo.MarkAnimationAttempt(file); err != nil {
			t.Fatal(err)
		}
	}
	if candidateIDs(t, repo)[file] != nil {
		t.Fatalf("still queued after %d attempts", maxAttempts)
	}
}

type fakeDownloader struct {
	calls atomic.Int64
	fail  map[string]bool
}

func (f *fakeDownloader) Download(_ context.Context, fileID string, _ int64) ([]byte, string, error) {
	f.calls.Add(1)
	if f.fail[fileID] {
		return nil, "", errors.New("telegram said no")
	}
	return []byte("source-" + fileID), "stickers/" + fileID + ".webm", nil
}

func TestWorkerRendersAndResumes(t *testing.T) {
	repo := newRepo(t)
	ok, broken := uniq("ok"), uniq("broken")
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: ok, FileID: ok, IsVideo: true})
	save(t, repo, &repository.Sticker{UserID: 1, StickerID: broken, FileID: broken, IsVideo: true})

	download := &fakeDownloader{fail: map[string]bool{broken: true}}
	var gotExtension string
	service := New(repo, download, func(_ context.Context, source []byte, ext string) ([]byte, error) {
		gotExtension = ext
		return append([]byte("preview:"), source...), nil
	})
	service.itemWait = 0

	service.runBatch(context.Background())

	preview, err := repo.GetAnimation(ok)
	if err != nil || !bytes.Equal(preview, []byte("preview:source-"+ok)) {
		t.Fatalf("preview not stored: %q, %v", preview, err)
	}
	if gotExtension != ".webm" {
		t.Fatalf("renderer must learn the container from Telegram's path, got %q", gotExtension)
	}
	if _, err := repo.GetAnimation(broken); err == nil {
		t.Fatal("a failed download must not leave a preview behind")
	}

	// A second pass is what a restart looks like: only the broken file is
	// tried again, the finished one is not downloaded twice.
	before := download.calls.Load()
	service.runBatch(context.Background())
	if candidateIDs(t, repo)[ok] != nil {
		t.Fatal("finished file came back into the queue")
	}
	if download.calls.Load()-before > 1 {
		t.Fatalf("restart redid finished work: %d downloads", download.calls.Load()-before)
	}
}

// Guards the real ffmpeg arguments: a video sticker with transparency has to
// come out as a multi-frame animated WebP that keeps its alpha channel.
func TestRenderProducesAnimatedWebPWithAlpha(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "sticker.webm")
	build := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-filter_complex",
		"color=black@0.0:s=512x512:d=2:r=30,format=rgba[bg];"+
			"color=yellow:s=100x100:d=2:r=30,format=rgba[box];"+
			"[bg][box]overlay=x='mod(t*200\\,400)':y=100:format=auto,format=yuva420p[v]",
		"-map", "[v]", "-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build a VP9+alpha fixture here: %v %s", err, out)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}

	preview, err := Render(context.Background(), raw, ".webm")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(preview, []byte("RIFF")) || !bytes.Contains(preview[:16], []byte("WEBP")) {
		t.Fatal("output is not a WebP")
	}
	if frames := bytes.Count(preview, []byte("ANMF")); frames < 10 {
		t.Fatalf("expected an animation, got %d frames", frames)
	}
	if !bytes.Contains(preview, []byte("ALPH")) {
		t.Fatal("transparency was lost: no ALPH chunk")
	}
}

// A "video" sticker can be a single frame lasting 1/30s. The fps filter used to
// drop it entirely, so the encoder failed on every attempt (seen in production).
func TestRenderSurvivesSingleFrameVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	source := filepath.Join(t.TempDir(), "one-frame.webm")
	build := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=512x219:r=30",
		"-frames:v", "1", "-c:v", "libvpx-vp9", source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build a one-frame fixture here: %v %s", err, out)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Render(context.Background(), raw, ".webm")
	if err != nil {
		t.Fatalf("a single-frame clip must still render: %v", err)
	}
	if !bytes.HasPrefix(preview, []byte("RIFF")) {
		t.Fatal("output is not a WebP")
	}
}
