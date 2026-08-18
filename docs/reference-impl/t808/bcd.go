package t808

import "fmt"

// encodePhoneBCD packs a digit string into 6 BCD bytes (12 digits, high
// nibble = tens digit of each byte-pair), left-padding with '0' as needed.
func encodePhoneBCD(digits string) ([6]byte, error) {
	return encodeBCD6(digits)
}

func decodePhoneBCD(b [6]byte) string { return decodeBCD(b[:]) }

// encodeBCD6 packs up to 12 decimal digits into 6 BCD bytes, left-padded.
func encodeBCD6(digits string) ([6]byte, error) {
	var out [6]byte
	packed, err := packBCD(digits, 6)
	if err != nil {
		return out, err
	}
	copy(out[:], packed)
	return out, nil
}

// packBCD left-pads digits with '0' to fill n bytes (2n digits) and packs
// two decimal digits per byte.
func packBCD(digits string, n int) ([]byte, error) {
	width := n * 2
	if len(digits) > width {
		return nil, fmt.Errorf("t808: %q has more than %d digits", digits, width)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return nil, fmt.Errorf("t808: %q is not all decimal digits", digits)
		}
	}
	padded := digits
	for len(padded) < width {
		padded = "0" + padded
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		hi := padded[i*2] - '0'
		lo := padded[i*2+1] - '0'
		out[i] = hi<<4 | lo
	}
	return out, nil
}

// decodeBCD unpacks BCD bytes into their decimal digit string.
func decodeBCD(b []byte) string {
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, '0'+(x>>4), '0'+(x&0x0F))
	}
	return string(out)
}

// BCDDateTime is the 6-byte YY-MM-DD-hh-mm-ss timestamp used by location
// reports, attendance, operation-request, and driving-plan messages.
type BCDDateTime struct {
	Year, Month, Day, Hour, Minute, Second int // Year is 2-digit (e.g. 24 for 2024)
}

func encodeBCDDateTime(t BCDDateTime) ([6]byte, error) {
	digits := fmt.Sprintf("%02d%02d%02d%02d%02d%02d", t.Year, t.Month, t.Day, t.Hour, t.Minute, t.Second)
	return encodeBCD6(digits)
}

func decodeBCDDateTime(b [6]byte) BCDDateTime {
	d := decodeBCD(b[:])
	return BCDDateTime{
		Year: atoi2(d[0:2]), Month: atoi2(d[2:4]), Day: atoi2(d[4:6]),
		Hour: atoi2(d[6:8]), Minute: atoi2(d[8:10]), Second: atoi2(d[10:12]),
	}
}

// BCDTime5 is the 5-byte hh-mm-ss-msms timestamp used by the CAN bus upload
// message's "data receiving time" field (no date component, includes a
// 4-digit millisecond value).
type BCDTime5 struct {
	Hour, Minute, Second, Millis int
}

func encodeBCDTime5(t BCDTime5) ([5]byte, error) {
	digits := fmt.Sprintf("%02d%02d%02d%04d", t.Hour, t.Minute, t.Second, t.Millis)
	packed, err := packBCD(digits, 5)
	if err != nil {
		return [5]byte{}, err
	}
	var out [5]byte
	copy(out[:], packed)
	return out, nil
}

func decodeBCDTime5(b [5]byte) BCDTime5 {
	d := decodeBCD(b[:])
	return BCDTime5{Hour: atoi2(d[0:2]), Minute: atoi2(d[2:4]), Second: atoi2(d[4:6]), Millis: atoi4(d[6:10])}
}

func atoi2(s string) int { return int(s[0]-'0')*10 + int(s[1]-'0') }
func atoi4(s string) int { return atoi2(s[0:2])*100 + atoi2(s[2:4]) }
