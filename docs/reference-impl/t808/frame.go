// Package t808 implements the binary OBU<->Server protocol described in
// "OBU_To_Server_Communication_Details_Rev_1.7.pdf" (a JT/T808-family
// dialect). Two aspects of the wire format are NOT documented anywhere in
// that source and are called out explicitly rather than silently guessed:
//
//  1. Byte-stuffing/escaping: the doc never says what happens if 0x7E (the
//     frame marker) appears inside the header or body. The wider JT/T808
//     family escapes 0x7E->0x7D02 and 0x7D->0x7D01; Build/Parse here do NOT
//     apply that by default since it's unconfirmed for this dialect, but
//     EscapeJT808/UnescapeJT808 are provided so callers can opt in once
//     confirmed against a real device.
//  2. Subpackaging: the "Message Attribute" word is documented only as
//     "message attributes with message length" with no bit layout (no
//     subpackage flag, no encryption flag). This codec treats the whole
//     word as a plain body-length value, matching every worked example in
//     the doc, and does not implement multi-packet fragmentation.
package t808

import (
	"errors"
	"fmt"
)

const marker = 0x7E

// Frame is one decoded OBU<->Server message.
type Frame struct {
	MsgID  uint16
	Phone  string // 12-digit BCD-decoded phone number, zero-padded (e.g. "008291915608")
	Serial uint16
	Body   []byte
}

// ErrMalformed is returned by Parse when raw doesn't start/end with the 0x7E
// marker or is too short to contain a full 12-byte header.
type ErrMalformed struct{ Reason string }

func (e *ErrMalformed) Error() string { return "t808: malformed frame: " + e.Reason }

// ErrChecksumMismatch is returned by Parse when the trailing XOR-8 check
// code doesn't match the recomputed value.
type ErrChecksumMismatch struct{ Want, Got byte }

func (e *ErrChecksumMismatch) Error() string {
	return fmt.Sprintf("t808: checksum mismatch: frame says 0x%02X, computed 0x%02X", e.Want, e.Got)
}

// checksum8 is "8 bit XOR all bytes", the rule stated verbatim for every
// message type in the source doc.
func checksum8(data []byte) byte {
	var x byte
	for _, b := range data {
		x ^= b
	}
	return x
}

// Build encodes a frame as Marker(1) Header(12) Body(N) CheckCode(1) Marker(1).
func Build(f Frame) ([]byte, error) {
	phoneBCD, err := encodePhoneBCD(f.Phone)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 14+len(f.Body))
	out = append(out, marker)
	out = appendU16(out, f.MsgID)
	out = appendU16(out, uint16(len(f.Body)))
	out = append(out, phoneBCD[:]...)
	out = appendU16(out, f.Serial)
	out = append(out, f.Body...)
	cs := checksum8(out[1:])
	out = append(out, cs, marker)
	return out, nil
}

// Parse decodes a raw frame. It accepts input with or without the leading/
// trailing 0x7E markers (mirrors the sumith package's Parse behavior).
func Parse(raw []byte) (Frame, error) {
	data := raw
	if len(raw) > 0 && raw[0] == marker {
		if raw[len(raw)-1] != marker {
			return Frame{}, &ErrMalformed{Reason: "starts with 0x7E but doesn't end with 0x7E"}
		}
		data = raw[1 : len(raw)-1]
	}
	if len(data) < 13 {
		return Frame{}, &ErrMalformed{Reason: "too short for a 12-byte header + check code"}
	}
	body := data[:len(data)-1]
	wantCS := data[len(data)-1]
	if len(body) < 12 {
		return Frame{}, &ErrMalformed{Reason: "too short for a 12-byte header"}
	}
	gotCS := checksum8(body)
	if gotCS != wantCS {
		return Frame{}, &ErrChecksumMismatch{Want: wantCS, Got: gotCS}
	}
	msgID := beU16(body[0:2])
	bodyLen := beU16(body[2:4])
	phone := decodePhoneBCD([6]byte(body[4:10]))
	serial := beU16(body[10:12])
	msgBody := body[12:]
	if int(bodyLen) != len(msgBody) {
		return Frame{}, &ErrMalformed{Reason: fmt.Sprintf("declared body length %d does not match actual %d", bodyLen, len(msgBody))}
	}
	return Frame{MsgID: msgID, Phone: phone, Serial: serial, Body: msgBody}, nil
}

// EscapeJT808 applies the standard JT/T808-family byte-stuffing rule
// (0x7E->0x7D 0x02, 0x7D->0x7D 0x01) to the header+body+checksum portion of
// a frame (i.e. everything Build produces between the two markers). This is
// NOT confirmed against the source doc for this specific dialect — see the
// package doc comment. Provided so callers can opt in once verified.
func EscapeJT808(unescaped []byte) []byte {
	out := make([]byte, 0, len(unescaped))
	for _, b := range unescaped {
		switch b {
		case 0x7E:
			out = append(out, 0x7D, 0x02)
		case 0x7D:
			out = append(out, 0x7D, 0x01)
		default:
			out = append(out, b)
		}
	}
	return out
}

// UnescapeJT808 reverses EscapeJT808.
func UnescapeJT808(escaped []byte) ([]byte, error) {
	out := make([]byte, 0, len(escaped))
	for i := 0; i < len(escaped); i++ {
		b := escaped[i]
		if b != 0x7D {
			out = append(out, b)
			continue
		}
		if i+1 >= len(escaped) {
			return nil, errors.New("t808: dangling 0x7D escape at end of input")
		}
		i++
		switch escaped[i] {
		case 0x02:
			out = append(out, 0x7E)
		case 0x01:
			out = append(out, 0x7D)
		default:
			return nil, fmt.Errorf("t808: invalid escape sequence 0x7D 0x%02X", escaped[i])
		}
	}
	return out, nil
}

func appendU16(b []byte, v uint16) []byte { return append(b, byte(v>>8), byte(v)) }
func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
func beU16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }
func beU32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
