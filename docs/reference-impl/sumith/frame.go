// Package sumith implements the Sumith Electronics "CCU-SCU Communication
// Protocol" (ITS System, Oct 2025, v1.8): a pipe-delimited ASCII protocol
// carried over serial/GPRS between an OBU (on-bus unit) and a CCU
// (server), plus a parallel set of plain-text SMS commands.
//
// Every frame has the same envelope: a leading '$', a header token (e.g.
// "LGN", "LOC", "101"; the same numeric token is reused with different
// meanings depending on direction — OBU->CCU "101" is SendFirmwareVersion,
// CCU->OBU "101" is Camera Main Stream Video Resolution — so callers must
// track direction themselves, which is why this package splits builders
// into obu2ccu.go and ccu2obu.go), comma-separated fields, a checksum field,
// and a trailing '#'.
//
// Checksum caveat: the doc labels this field "32-bit Checksum" but its own
// worked examples are inconsistent — some render it as a decimal number
// (e.g. "2448041353"), most render it as 8 lowercase hex digits (e.g.
// "70bf1fd4", "9e770805"). That's not enough to pin down the exact
// algorithm (candidates: CRC-32/IEEE, a 32-bit running XOR/sum, etc.) from
// the document alone. This package defaults to CRC-32/IEEE rendered as 8
// lowercase hex digits (the majority convention in the examples) and makes
// the checksum function pluggable via WithChecksum so it can be swapped for
// whatever a real device/server actually implements once verified against
// live traffic.
package sumith

import (
	"fmt"
	"hash/crc32"
	"strings"
)

// ChecksumFunc computes a frame's checksum over its content bytes (the
// header token plus fields, comma-joined, with no leading '$' or trailing
// '#'/checksum).
type ChecksumFunc func(content []byte) string

// CRC32HexChecksum is the default ChecksumFunc: CRC-32/IEEE rendered as 8
// lowercase hex digits. See the package doc comment's checksum caveat.
func CRC32HexChecksum(content []byte) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE(content))
}

// Codec builds and parses Sumith frames with a configurable checksum
// function.
type Codec struct {
	Checksum ChecksumFunc
}

// NewCodec returns a Codec using CRC32HexChecksum. Pass a different
// ChecksumFunc via Codec.Checksum = ... if you've verified the real
// algorithm against device traffic.
func NewCodec() *Codec {
	return &Codec{Checksum: CRC32HexChecksum}
}

// Build assembles a frame: "$" + token + "," + join(fields, ",") + "," +
// checksum + "#". token is the header (e.g. "LGN", "101", "SETAPN"); fields
// are already-stringified values in wire order.
func (c *Codec) Build(token string, fields ...string) string {
	content := token
	if len(fields) > 0 {
		content += "," + strings.Join(fields, ",")
	}
	chk := c.checksumFunc()([]byte(content))
	return "$" + content + "," + chk + "#"
}

// Frame is a parsed, unverified Sumith packet.
type Frame struct {
	Token    string   // header token, e.g. "LGN", "LOC", "101"
	Fields   []string // fields between the token and the checksum, in wire order
	Checksum string   // as received (not yet verified against Content)
	Content  string   // token + "," + join(fields, ",") — what the checksum should cover
}

// ErrMalformed is returned by Parse when raw doesn't have the "$...#"
// envelope or has fewer than 2 comma-separated parts (token + checksum).
type ErrMalformed struct{ Raw string }

func (e *ErrMalformed) Error() string { return "sumith: malformed frame: " + e.Raw }

// ErrChecksumMismatch is returned by ParseVerify when the computed checksum
// doesn't match the frame's Checksum field.
type ErrChecksumMismatch struct {
	Frame    Frame
	Expected string
}

func (e *ErrChecksumMismatch) Error() string {
	return fmt.Sprintf("sumith: checksum mismatch: frame has %q, computed %q (see package doc's checksum caveat)", e.Frame.Checksum, e.Expected)
}

// Parse splits raw ("$TOKEN,f1,f2,...,checksum#" or just the inner content
// without the markers) into a Frame without verifying the checksum.
func Parse(raw string) (Frame, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "$")
	trimmed = strings.TrimSuffix(trimmed, "#")

	parts := strings.Split(trimmed, ",")
	if len(parts) < 2 {
		return Frame{}, &ErrMalformed{Raw: raw}
	}

	token := parts[0]
	checksum := parts[len(parts)-1]
	fields := parts[1 : len(parts)-1]
	content := strings.Join(parts[:len(parts)-1], ",")

	return Frame{Token: token, Fields: fields, Checksum: checksum, Content: content}, nil
}

// ParseVerify parses raw and verifies its checksum using c's ChecksumFunc,
// returning *ErrChecksumMismatch (wrapping the parsed Frame) if it doesn't
// match.
func (c *Codec) ParseVerify(raw string) (Frame, error) {
	f, err := Parse(raw)
	if err != nil {
		return Frame{}, err
	}
	want := c.checksumFunc()([]byte(f.Content))
	if want != f.Checksum {
		return f, &ErrChecksumMismatch{Frame: f, Expected: want}
	}
	return f, nil
}

func (c *Codec) checksumFunc() ChecksumFunc {
	if c == nil || c.Checksum == nil {
		return CRC32HexChecksum
	}
	return c.Checksum
}

// Field is a small helper for callers building fixed-position field lists:
// it returns v unless v is empty, then returns fallback for convenience.
func Field(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
