package player

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

// Encoding settings. They match the camera budget: every viewer receives this stream.
// A keyframe every 2 s lets someone who joins mid-loop see a picture quickly: a pre-encoded
// file cannot produce a keyframe on request.
var (
	videoArgs = []string{"-an",
		"-vf", "scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,fps=15",
		"-c:v", "libvpx", "-b:v", "450k", "-deadline", "good", "-cpu-used", "4",
		"-g", "30", "-keyint_min", "30", "-auto-alt-ref", "0", "-f", "ivf"}
	audioArgs = []string{"-vn",
		"-c:a", "libopus", "-b:a", "64k", "-ar", "48000", "-ac", "2",
		"-frame_duration", "20", "-page_duration", "20000", "-f", "ogg"}
	settingsVersion = "v1" // bump when the arguments above change, to re-encode cached files
)

// Media is a transcoded file. Audio is empty for a file without sound.
type Media struct {
	Video, Audio         string
	OpenVideo, OpenAudio Opener
}

// Transcode encodes src once into cacheDir (VP8 in IVF, Opus in Ogg) and reuses the result
// while the file's name, size and modification time and the settings stay the same.
func Transcode(ctx context.Context, src, cacheDir string) (Media, error) {
	st, err := os.Stat(src)
	if err != nil {
		return Media{}, err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s", src, st.Size(), st.ModTime().UnixNano(), settingsVersion)))
	key := hex.EncodeToString(sum[:8])
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return Media{}, err
	}
	m := Media{Video: filepath.Join(cacheDir, key+".ivf")}
	if err := encode(ctx, src, m.Video, videoArgs); err != nil {
		return Media{}, fmt.Errorf("video: %w", err)
	}
	m.OpenVideo = func() (Source, error) { return openIVF(m.Video) }
	audio := filepath.Join(cacheDir, key+".ogg")
	switch err := encode(ctx, src, audio, audioArgs); {
	case errors.Is(err, errNoStream):
		// a file without sound plays silently
	case err != nil:
		return Media{}, fmt.Errorf("audio: %w", err)
	default:
		m.Audio = audio
		m.OpenAudio = func() (Source, error) { return openOgg(m.Audio) }
	}
	return m, nil
}

var errNoStream = errors.New("no such stream in the file")

// encode runs ffmpeg into a temporary file and renames it, so a half-written file is never cached.
func encode(ctx context.Context, src, dst string, args []string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if _, err := os.Stat(dst + ".none"); err == nil {
		return errNoStream // known to have no such stream
	}
	tmp := dst + ".part"
	full := append([]string{"-nostdin", "-y", "-loglevel", "error", "-i", src}, args...)
	full = append(full, tmp)
	out, err := exec.CommandContext(ctx, "ffmpeg", full...).CombinedOutput()
	if err != nil {
		os.Remove(tmp)
		if bytes.Contains(out, []byte("does not contain any stream")) || bytes.Contains(out, []byte("Output file is empty")) {
			_ = os.WriteFile(dst+".none", nil, 0o600)
			return errNoStream
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, bytes.TrimSpace(out))
	}
	if st, err := os.Stat(tmp); err == nil && st.Size() == 0 {
		os.Remove(tmp)
		_ = os.WriteFile(dst+".none", nil, 0o600)
		return errNoStream
	}
	return os.Rename(tmp, dst)
}

// ivfSource reads VP8 frames. The file is encoded at a constant frame rate (fps=15 above), so
// every frame lasts one tick of the header's timebase; pion's frame timestamps are not in those
// units, so they are not used.
type ivfSource struct {
	f    *os.File
	r    *ivfreader.IVFReader
	tick time.Duration
}

func openIVF(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, h, err := ivfreader.NewWith(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if h.TimebaseDenominator == 0 {
		f.Close()
		return nil, errors.New("ivf: zero timebase")
	}
	return &ivfSource{f: f, r: r, tick: time.Duration(float64(time.Second) * float64(h.TimebaseNumerator) / float64(h.TimebaseDenominator))}, nil
}

func (s *ivfSource) Next() (Sample, error) {
	frame, _, err := s.r.ParseNextFrame()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Sample{}, io.EOF
		}
		return Sample{}, err
	}
	return Sample{Data: frame, Duration: s.tick}, nil
}

func (s *ivfSource) Close() error {
	if s.f == nil { // a live source reads ffmpeg's pipe; Live closes it
		return nil
	}
	return s.f.Close()
}

// oggSource reads Opus packets, one per page (ffmpeg writes them so with -page_duration 20000);
// a page lasts as long as its granule position advanced, in 48 kHz samples.
type oggSource struct {
	f       *os.File
	r       *oggreader.OggReader
	granule uint64
}

func openOgg(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, _, err := oggreader.NewWith(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &oggSource{f: f, r: r}, nil
}

func (s *oggSource) Next() (Sample, error) {
	for {
		page, h, err := s.r.ParseNextPage()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return Sample{}, io.EOF
			}
			return Sample{}, err
		}
		if h.GranulePosition <= s.granule || bytes.HasPrefix(page, []byte("OpusTags")) {
			continue // header pages carry no audio
		}
		d := time.Duration(h.GranulePosition-s.granule) * time.Second / 48000
		s.granule = h.GranulePosition
		return Sample{Data: page, Duration: d}, nil
	}
}

func (s *oggSource) Close() error { return s.f.Close() }
