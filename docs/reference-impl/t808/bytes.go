package t808

import (
	"bytes"
	"fmt"
)

// byteReader walks a body []byte by position, accumulating the first error
// (mirrors the sumith package's fieldReader pattern) so callers check err
// once at the end instead of after every access.
type byteReader struct {
	b   []byte
	pos int
	err error
}

func newByteReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) need(n int) []byte {
	if r.err != nil {
		return nil
	}
	if r.pos+n > len(r.b) {
		r.err = fmt.Errorf("t808: need %d bytes at offset %d, only %d remain", n, r.pos, len(r.b)-r.pos)
		return nil
	}
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v
}

func (r *byteReader) u8() byte {
	v := r.need(1)
	if v == nil {
		return 0
	}
	return v[0]
}

func (r *byteReader) u16() uint16 {
	v := r.need(2)
	if v == nil {
		return 0
	}
	return beU16(v)
}

func (r *byteReader) u32() uint32 {
	v := r.need(4)
	if v == nil {
		return 0
	}
	return beU32(v)
}

func (r *byteReader) bytesN(n int) []byte {
	v := r.need(n)
	if v == nil {
		return nil
	}
	out := make([]byte, n)
	copy(out, v)
	return out
}

func (r *byteReader) bcd(n int) string {
	v := r.need(n)
	if v == nil {
		return ""
	}
	return decodeBCD(v)
}

// cstring reads a NULL-terminated string. If no NULL byte remains, it
// consumes the rest of the buffer (some worked examples in the doc omit the
// trailing NULL on the final field of a frame).
func (r *byteReader) cstring() string {
	if r.err != nil {
		return ""
	}
	idx := bytes.IndexByte(r.b[r.pos:], 0x00)
	if idx < 0 {
		s := string(r.b[r.pos:])
		r.pos = len(r.b)
		return s
	}
	s := string(r.b[r.pos : r.pos+idx])
	r.pos += idx + 1
	return s
}

// remaining returns all unconsumed bytes.
func (r *byteReader) remaining() []byte {
	if r.err != nil || r.pos >= len(r.b) {
		return nil
	}
	out := r.b[r.pos:]
	r.pos = len(r.b)
	return out
}

func (r *byteReader) atEnd() bool { return r.err == nil && r.pos >= len(r.b) }

// byteWriter is the write-side counterpart, building up a message body.
type byteWriter struct {
	buf bytes.Buffer
	err error
}

func (w *byteWriter) u8(v byte)    { w.buf.WriteByte(v) }
func (w *byteWriter) u16(v uint16) { w.buf.Write([]byte{byte(v >> 8), byte(v)}) }
func (w *byteWriter) u32(v uint32) {
	w.buf.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
func (w *byteWriter) raw(b []byte) { w.buf.Write(b) }
func (w *byteWriter) cstring(s string) {
	w.buf.WriteString(s)
	w.buf.WriteByte(0x00)
}
func (w *byteWriter) bcd6(digits string) {
	b, err := encodeBCD6(digits)
	if err != nil && w.err == nil {
		w.err = err
		return
	}
	w.buf.Write(b[:])
}
func (w *byteWriter) dateTime(t BCDDateTime) {
	b, err := encodeBCDDateTime(t)
	if err != nil && w.err == nil {
		w.err = err
		return
	}
	w.buf.Write(b[:])
}
func (w *byteWriter) bytesOut() ([]byte, error) {
	if w.err != nil {
		return nil, w.err
	}
	return w.buf.Bytes(), nil
}
