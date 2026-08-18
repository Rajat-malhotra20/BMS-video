package sumith

import "testing"

func TestBuildParseRoundTrip(t *testing.T) {
	c := NewCodec()
	raw := c.Build("LGN", "AP36TS1234", "864819050843380", "11.0.20", "1.0.2")

	if raw[0] != '$' || raw[len(raw)-1] != '#' {
		t.Fatalf("expected $...# envelope, got %q", raw)
	}

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	if f.Token != "LGN" {
		t.Fatalf("expected token LGN, got %q", f.Token)
	}
	if len(f.Fields) != 4 || f.Fields[0] != "AP36TS1234" {
		t.Fatalf("unexpected fields: %+v", f.Fields)
	}
}

func TestParseVerifyDetectsTamperedChecksum(t *testing.T) {
	c := NewCodec()
	raw := c.Build("LOC", "3339", "NR")
	tampered := raw[:len(raw)-2] + "0#" // corrupt the last checksum char before '#'

	_, err := c.ParseVerify(tampered)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, ok := err.(*ErrChecksumMismatch); !ok {
		t.Fatalf("expected *ErrChecksumMismatch, got %T: %v", err, err)
	}
}

func TestParseMalformedFrame(t *testing.T) {
	if _, err := Parse("not-a-frame"); err == nil {
		t.Fatal("expected error for malformed frame")
	}
}

func TestParseWithoutMarkers(t *testing.T) {
	c := NewCodec()
	raw := c.Build("HLT", "1", "2")
	inner := raw[1 : len(raw)-1] // strip $ and #

	f, err := Parse(inner)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Token != "HLT" {
		t.Fatalf("expected token HLT, got %q", f.Token)
	}
}
