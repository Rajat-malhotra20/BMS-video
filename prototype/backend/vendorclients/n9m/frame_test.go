package n9m

import (
	"bytes"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{
		Version:     1,
		Encrypted:   false,
		Compressed:  false,
		PayloadType: PayloadCommand,
		SSRC:        0,
		CSRC:        nil,
	}
	payload := []byte(`{"MODULE":"CERTIFICATE","OPERATION":"KEEPALIVE"}`)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, h, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	frame, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if frame.Header.Version != 1 {
		t.Fatalf("expected version 1, got %d", frame.Header.Version)
	}
	if frame.Header.PayloadType != PayloadCommand {
		t.Fatalf("expected PayloadCommand, got %d", frame.Header.PayloadType)
	}
	if frame.Header.PayloadLen != uint32(len(payload)) {
		t.Fatalf("expected payload len %d, got %d", len(payload), frame.Header.PayloadLen)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("payload mismatch: got %q", frame.Payload)
	}
}

func TestHeaderRoundTripWithCSRCAndSpecialSSRC(t *testing.T) {
	h := Header{
		Version:     1,
		PayloadType: PayloadSpecial,
		SSRC:        SpecialGPSUpload,
		CSRC:        []uint32{0xDEADBEEF, 0x00000001},
	}
	payload := []byte("gps-binary-blob")

	var buf bytes.Buffer
	if err := WriteFrame(&buf, h, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	frame, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if frame.Header.CSRCCount != 2 || len(frame.Header.CSRC) != 2 {
		t.Fatalf("expected 2 CSRC entries, got %+v", frame.Header.CSRC)
	}
	if frame.Header.CSRC[0] != 0xDEADBEEF {
		t.Fatalf("csrc[0] mismatch: %x", frame.Header.CSRC[0])
	}
	if frame.Header.SSRC != SpecialGPSUpload {
		t.Fatalf("expected SSRC %d, got %d", SpecialGPSUpload, frame.Header.SSRC)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("payload mismatch: got %q", frame.Payload)
	}
}

func TestEncodeRejectsMismatchedCSRCCount(t *testing.T) {
	h := Header{CSRCCount: 2, CSRC: []uint32{1}}
	var buf bytes.Buffer
	if err := h.Encode(&buf); err == nil {
		t.Fatal("expected error for mismatched CSRCCount")
	}
}
