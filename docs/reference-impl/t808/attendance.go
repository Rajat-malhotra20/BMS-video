package t808

const MsgIDAttendanceRequest = 0x0B05

// Attendance type codes (Table 31 / attendance).
const (
	AttendanceOnDuty  = 0x01
	AttendanceOffDuty = 0x02
	AttendanceSignIn  = 0x03
	AttendanceSignOut = 0x04
	AttendanceCheck   = 0x05
)

// Attendance method codes (Table 32 / attendance).
const (
	AttendanceMethodEmployeeCard = 0x01
	AttendanceMethodID           = 0x02
	AttendanceMethodDriver       = 0x03
	AttendanceMethodTicket       = 0x04
)

// AttendanceRequest is message 0x0B05 (OBU -> Server).
type AttendanceRequest struct {
	RouteNumber uint32
	EmployeeID  string
	Time        BCDDateTime
	Type        byte
	Method      byte
	Password    string
}

func BuildAttendanceRequest(m AttendanceRequest) ([]byte, error) {
	var w byteWriter
	w.u32(m.RouteNumber)
	w.cstring(m.EmployeeID)
	w.dateTime(m.Time)
	w.u8(m.Type)
	w.u8(m.Method)
	w.cstring(m.Password)
	return w.bytesOut()
}

func DecodeAttendanceRequest(body []byte) (AttendanceRequest, error) {
	r := newByteReader(body)
	m := AttendanceRequest{RouteNumber: r.u32(), EmployeeID: r.cstring()}
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.Time = decodeBCDDateTime(t)
	m.Type = r.u8()
	m.Method = r.u8()
	m.Password = r.cstring()
	return m, r.err
}

const MsgIDAttendanceResponse = 0x8B05

// Attendance response codes (Table 60).
const (
	AttendanceRespInvalidCard = 0x00
	AttendanceRespOnDuty      = 0x01
	AttendanceRespOffDuty     = 0x02
	AttendanceRespCheckIn     = 0x03
	AttendanceRespCheckOut    = 0x04
	AttendanceRespInspection  = 0x05
)

// AttendanceResponse is message 0x8B05 (Server -> OBU).
type AttendanceResponse struct {
	Response       byte
	ResponseTime   BCDDateTime
	ResponseString string
}

func BuildAttendanceResponse(m AttendanceResponse) ([]byte, error) {
	var w byteWriter
	w.u8(m.Response)
	w.dateTime(m.ResponseTime)
	w.cstring(m.ResponseString)
	return w.bytesOut()
}

func DecodeAttendanceResponse(body []byte) (AttendanceResponse, error) {
	r := newByteReader(body)
	m := AttendanceResponse{Response: r.u8()}
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.ResponseTime = decodeBCDDateTime(t)
	m.ResponseString = r.cstring()
	return m, r.err
}

const MsgIDTextMessage = 0x8300

// Text message info-flag bits (Table 38).
const (
	TextFlagEmergency          = 1 << 0
	TextFlagOBUDisplay         = 1 << 2
	TextFlagOBUTTSBroadcast    = 1 << 3
	TextFlagAdvertisingDisplay = 1 << 4
	TextFlagCANFaultInfo       = 1 << 5 // 0 = Centre Navigation info
)

// TextMessage is message 0x8300 (Server -> OBU), used among other things to
// confirm attendance sign-in/out to the driver's display/TTS.
type TextMessage struct {
	Flags byte
	Text  string
}

func BuildTextMessage(m TextMessage) ([]byte, error) {
	var w byteWriter
	w.u8(m.Flags)
	w.cstring(m.Text)
	return w.bytesOut()
}

func DecodeTextMessage(body []byte) (TextMessage, error) {
	r := newByteReader(body)
	m := TextMessage{Flags: r.u8(), Text: r.cstring()}
	return m, r.err
}
