package bridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// ErrFFmpegExit wraps a failed ffmpeg run with its captured stderr tail, so
// Supervisor.List()/Status.LastErr surfaces something actionable instead of
// a bare "exit status 1".
type ErrFFmpegExit struct {
	Args   []string
	Stderr string
	Cause  error
}

func (e *ErrFFmpegExit) Error() string {
	stderr := e.Stderr
	if len(stderr) > 500 {
		stderr = "..." + stderr[len(stderr)-500:]
	}
	return fmt.Sprintf("ffmpeg %v: %v: %s", e.Args, e.Cause, stderr)
}

func (e *ErrFFmpegExit) Unwrap() error { return e.Cause }

const stderrCaptureLimit = 8 << 10 // 8KiB tail, enough for ffmpeg's final error lines

// runFFmpeg runs ffmpeg with args until it exits or ctx is canceled (in
// which case the process is killed and ctx.Err() is returned).
func runFFmpeg(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: stderrCaptureLimit}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("bridge: start ffmpeg: %w", err)
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return &ErrFFmpegExit{Args: args, Stderr: stderr.String(), Cause: err}
	}
	return nil
}

// limitedWriter keeps only the last `limit` bytes written to it, so a
// long-running ffmpeg process doesn't grow an unbounded stderr buffer.
type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if extra := w.buf.Len() - w.limit; extra > 0 {
		w.buf.Next(extra)
	}
	return len(p), nil
}

// RemuxToRTSP builds a RunFunc that remuxes (no re-encode) sourceURL into
// rtspOut as an RTSP publish. sourceURL may be an FLV/HTTP live URL (from
// Castmaster's LiveVideoURL) or an HLS (.m3u8) URL (from HistoryStreamURL).
// Requires the `ffmpeg` binary on PATH.
func RemuxToRTSP(sourceURL, rtspOut string) RunFunc {
	return func(ctx context.Context) error {
		return runFFmpeg(ctx,
			"-hide_banner", "-loglevel", "warning", "-y",
			"-i", sourceURL,
			"-c", "copy",
			"-f", "rtsp", "-rtsp_transport", "tcp",
			rtspOut,
		)
	}
}

// StdinFormat selects the elementary-stream framing ffmpeg should expect on
// stdin when bridging raw N9M media-channel bytes (PayloadLiveVideo).
type StdinFormat string

const (
	// StdinFormatH264 assumes Annex-B H.264 (0x00000001-prefixed NAL units),
	// the common case for these device families.
	StdinFormatH264 StdinFormat = "h264"
	// StdinFormatH265 is HEVC Annex-B.
	StdinFormatH265 StdinFormat = "hevc"
)

// StreamStdinToRTSP builds a RunFunc that reads a raw elementary stream from
// r (fed by copying n9m.Conn.Media frame payloads for PayloadLiveVideo) and
// republishes it to rtspOut. r's producer must keep writing for the life of
// the RunFunc; if r returns io.EOF the ffmpeg process exits and the
// Supervisor will restart this RunFunc, re-invoking it with the same r — so
// callers needing restart-on-failure should instead supply a reader that
// itself reconnects, or manage a single long run via a fresh RunFunc per
// media-channel connection.
func StreamStdinToRTSP(r io.Reader, format StdinFormat, rtspOut string) RunFunc {
	return func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "ffmpeg",
			"-hide_banner", "-loglevel", "warning", "-y",
			"-f", string(format), "-i", "pipe:0",
			"-c", "copy",
			"-f", "rtsp", "-rtsp_transport", "tcp",
			rtspOut,
		)
		var stderr bytes.Buffer
		cmd.Stdin = r
		cmd.Stdout = io.Discard
		cmd.Stderr = &limitedWriter{buf: &stderr, limit: stderrCaptureLimit}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("bridge: start ffmpeg: %w", err)
		}
		err := cmd.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return &ErrFFmpegExit{Args: cmd.Args, Stderr: stderr.String(), Cause: err}
		}
		return nil
	}
}
