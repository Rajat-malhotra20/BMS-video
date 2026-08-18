package t808

import "testing"

func TestBuildParseRoundTrip(t *testing.T) {
	f := Frame{MsgID: 0x0100, Phone: "008291915608", Serial: 6, Body: []byte{0x01, 0x02, 0x03}}
	raw, err := Build(f)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if raw[0] != marker || raw[len(raw)-1] != marker {
		t.Fatalf("expected marker envelope, got % X", raw)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.MsgID != f.MsgID || got.Phone != f.Phone || got.Serial != f.Serial || string(got.Body) != string(f.Body) {
		t.Fatalf("round trip mismatch: want %+v got %+v", f, got)
	}
}

func TestParseDetectsTamperedChecksum(t *testing.T) {
	f := Frame{MsgID: 0x0200, Phone: "018664981864", Serial: 1, Body: []byte{0xAA, 0xBB}}
	raw, _ := Build(f)
	raw[len(raw)-2] ^= 0xFF // corrupt check code
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, ok := err.(*ErrChecksumMismatch); !ok {
		t.Fatalf("expected *ErrChecksumMismatch, got %T: %v", err, err)
	}
}

func TestParseMalformedFrame(t *testing.T) {
	if _, err := Parse([]byte{marker, 0x01, marker}); err == nil {
		t.Fatal("expected error for too-short frame")
	}
}

func TestParseWithoutMarkers(t *testing.T) {
	f := Frame{MsgID: 0x8001, Phone: "008291915608", Serial: 5, Body: []byte{0x00, 0x01, 0x01, 0x02, 0x00}}
	raw, _ := Build(f)
	inner := raw[1 : len(raw)-1]
	got, err := Parse(inner)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.MsgID != f.MsgID {
		t.Fatalf("expected MsgID 0x8001, got 0x%04X", got.MsgID)
	}
}

func TestEscapeUnescapeRoundTrip(t *testing.T) {
	raw := []byte{0x01, 0x7E, 0x02, 0x7D, 0x03}
	escaped := EscapeJT808(raw)
	for _, b := range escaped {
		if b == 0x7E {
			t.Fatalf("escaped output must not contain raw 0x7E: % X", escaped)
		}
	}
	got, err := UnescapeJT808(escaped)
	if err != nil {
		t.Fatalf("UnescapeJT808: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("escape round trip mismatch: want % X got % X", raw, got)
	}
}

func TestBCDPhoneRoundTrip(t *testing.T) {
	phone := "008291915608"
	b, err := encodePhoneBCD(phone)
	if err != nil {
		t.Fatalf("encodePhoneBCD: %v", err)
	}
	if got := decodePhoneBCD(b); got != phone {
		t.Fatalf("BCD round trip: want %q got %q", phone, got)
	}
}
