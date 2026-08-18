package n9mserver

import (
	"io"

	"mediamtx-console/vendorclients/n9m"
)

// MediaFrameReader adapts a *n9m.Conn's Media channel into an io.Reader,
// concatenating the payload bytes of frames matching want (typically
// n9m.PayloadLiveVideo) and discarding any others. It's meant to be handed
// to bridge.StreamStdinToRTSP as the source ffmpeg reads from.
type MediaFrameReader struct {
	conn *n9m.Conn
	want n9m.PayloadType
	buf  []byte
}

// NewMediaFrameReader wraps conn's Media channel, yielding only frames whose
// PayloadType == want.
func NewMediaFrameReader(conn *n9m.Conn, want n9m.PayloadType) *MediaFrameReader {
	return &MediaFrameReader{conn: conn, want: want}
}

// Read implements io.Reader. It returns io.EOF once conn's Media channel is
// closed (i.e. the media connection's read loop exited).
func (r *MediaFrameReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		frame, ok := <-r.conn.Media
		if !ok {
			return 0, io.EOF
		}
		if frame.Header.PayloadType != r.want {
			continue
		}
		r.buf = frame.Payload
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
