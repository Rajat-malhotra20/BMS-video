package t808

const MsgIDAuthRequest = 0x0102

// AuthRequest is message 0x0102 (OBU -> Server): the OBU echoes back the
// verification code it received in RegisterResponse.
type AuthRequest struct {
	VerificationCode string
}

func BuildAuthRequest(m AuthRequest) []byte { return []byte(m.VerificationCode) }

func DecodeAuthRequest(body []byte) (AuthRequest, error) {
	return AuthRequest{VerificationCode: string(body)}, nil
}

// General response result codes (message 0x8001).
const (
	GeneralResultSuccess     = 0x00
	GeneralResultFailed      = 0x01
	GeneralResultError       = 0x02
	GeneralResultUnsupported = 0x03
	GeneralResultAlarm       = 0x04
)

const MsgIDGeneralResponse = 0x8001

// GeneralResponse is message 0x8001 (Server -> OBU): a fixed 5-byte body
// acknowledging any prior OBU-originated message by its serial + message ID.
type GeneralResponse struct {
	AnsweringSerial uint16
	AnsweringID     uint16
	Result          byte
}

func BuildGeneralResponse(m GeneralResponse) []byte {
	var w byteWriter
	w.u16(m.AnsweringSerial)
	w.u16(m.AnsweringID)
	w.u8(m.Result)
	b, _ := w.bytesOut() // byteWriter's only possible error source is BCD encoding, unused here
	return b
}

func DecodeGeneralResponse(body []byte) (GeneralResponse, error) {
	r := newByteReader(body)
	m := GeneralResponse{AnsweringSerial: r.u16(), AnsweringID: r.u16(), Result: r.u8()}
	return m, r.err
}
