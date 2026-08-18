package t808

// Plate colour values for RegisterRequest.PlateColor (section on 0x0100).
const (
	PlateColorNull   = 0x00
	PlateColorBlue   = 0x01
	PlateColorYellow = 0x02
)

// RegisterRequest is message 0x0100 (OBU -> Server).
type RegisterRequest struct {
	ProvinceID     uint16
	CityID         uint16
	ManufacturerID string // 5 ASCII bytes, e.g. "24373"
	TerminalID     string // fixed 20-byte field, e.g. "CHEMITO" zero-padded
	PlateColor     byte
	// VehicleIDOrPlate holds the VIN when PlateColor == PlateColorNull,
	// otherwise the vehicle registration/plate number.
	VehicleIDOrPlate string
}

const MsgIDRegisterRequest = 0x0100

func BuildRegisterRequest(m RegisterRequest) ([]byte, error) {
	var w byteWriter
	w.u16(m.ProvinceID)
	w.u16(m.CityID)
	mfr := padRight(m.ManufacturerID, 5)
	w.raw([]byte(mfr))
	term := padRight(m.TerminalID, 20)
	w.raw([]byte(term))
	w.u8(m.PlateColor)
	w.raw([]byte(m.VehicleIDOrPlate))
	return w.bytesOut()
}

func DecodeRegisterRequest(body []byte) (RegisterRequest, error) {
	r := newByteReader(body)
	m := RegisterRequest{
		ProvinceID:     r.u16(),
		CityID:         r.u16(),
		ManufacturerID: trimZero(string(r.bytesN(5))),
		TerminalID:     trimZero(string(r.bytesN(20))),
	}
	m.PlateColor = r.u8()
	m.VehicleIDOrPlate = string(r.remaining())
	return m, r.err
}

// Register response result codes (message 0x8100).
const (
	RegisterResultSuccess           = 0x00
	RegisterResultVehicleRegistered = 0x01
	RegisterResultNoVehicleData     = 0x02
	RegisterResultOBURegistered     = 0x03
	RegisterResultNoOBUData         = 0x04
)

const MsgIDRegisterResponse = 0x8100

// RegisterResponse is message 0x8100 (Server -> OBU).
type RegisterResponse struct {
	AnsweringSerial uint16
	Result          byte
	// VerificationCode is only meaningful when Result == RegisterResultSuccess.
	// The doc's worked example terminates it with a trailing 0x7C byte before
	// the check code; that terminator is preserved verbatim here rather than
	// modeled as a NULL terminator, since 0x7C ('|') is what the example uses.
	VerificationCode string
}

func BuildRegisterResponse(m RegisterResponse) ([]byte, error) {
	var w byteWriter
	w.u16(m.AnsweringSerial)
	w.u8(m.Result)
	if m.Result == RegisterResultSuccess {
		w.raw([]byte(m.VerificationCode))
		w.u8('|')
	}
	return w.bytesOut()
}

func DecodeRegisterResponse(body []byte) (RegisterResponse, error) {
	r := newByteReader(body)
	m := RegisterResponse{AnsweringSerial: r.u16(), Result: r.u8()}
	if m.Result == RegisterResultSuccess {
		rest := r.remaining()
		if len(rest) > 0 && rest[len(rest)-1] == '|' {
			rest = rest[:len(rest)-1]
		}
		m.VerificationCode = string(rest)
	}
	return m, r.err
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	b := make([]byte, n)
	copy(b, s)
	return string(b)
}

func trimZero(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x00 {
			return s[:i]
		}
	}
	return s
}
