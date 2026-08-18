package sumith

import (
	"fmt"
	"strconv"
	"time"
)

// FormatDateTime renders t in the CCU->OBU wire format the doc calls
// "YYMMDDHHMMSS" (2-digit year first). Every CCU->OBU command's DateTime
// field uses this format; OBU->CCU messages use their own, different
// per-message date/time formats (see each Decode* function's doc comment).
func FormatDateTime(t time.Time) string {
	return t.Format("060102150405")
}

// fieldReader walks a Frame's Fields by position, accumulating the first
// error encountered so callers can check it once at the end instead of
// after every access (the same pattern bufio.Scanner/binary.Read use).
type fieldReader struct {
	fields []string
	pos    int
	err    error
}

func newFieldReader(f Frame) *fieldReader { return &fieldReader{fields: f.Fields} }

func (r *fieldReader) next() string {
	if r.err != nil {
		return ""
	}
	if r.pos >= len(r.fields) {
		r.err = fmt.Errorf("sumith: expected field at index %d, only %d present", r.pos, len(r.fields))
		return ""
	}
	v := r.fields[r.pos]
	r.pos++
	return v
}

func (r *fieldReader) nextInt() int {
	v := r.next()
	if r.err != nil {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.err = fmt.Errorf("sumith: field %d (%q): %w", r.pos-1, v, err)
	}
	return n
}

func (r *fieldReader) nextFloat() float64 {
	v := r.next()
	if r.err != nil {
		return 0
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.err = fmt.Errorf("sumith: field %d (%q): %w", r.pos-1, v, err)
	}
	return n
}

// rest returns all remaining fields, useful for trailing variable-length
// lists (e.g. CSV sub-lists within a field aren't split further here).
func (r *fieldReader) rest() []string {
	if r.err != nil || r.pos >= len(r.fields) {
		return nil
	}
	out := r.fields[r.pos:]
	r.pos = len(r.fields)
	return out
}

func itoa(n int) string     { return strconv.Itoa(n) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func boolFlag01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
