package t808

import "fmt"

const MsgIDOperationRequest = 0x0B09

// Operation request codes (Table 42).
const (
	OpSchedulingRequest = 0x01
	OpOffDutyRequest    = 0x02
	OpRefuelRequest     = 0x03
	OpAeratingRequest   = 0x04
	OpChargingRequest   = 0x05
	OpExitOperation     = 0x06
	OpStartManually     = 0x07
	OpEndManually       = 0x08
	OpCharterService    = 0x09
	OpRepairRequest     = 0x0A
	OpOtherRequest      = 0x0B
	OpStartOperation    = 0x0C
	OpRequestIntercom   = 0x0D
)

// OperationRequest is message 0x0B09 (OBU -> Server).
type OperationRequest struct {
	RouteNumber uint32
	EmployeeID  string
	RequestCode byte
	Time        BCDDateTime
}

func BuildOperationRequest(m OperationRequest) ([]byte, error) {
	var w byteWriter
	w.u32(m.RouteNumber)
	w.cstring(m.EmployeeID)
	w.u8(m.RequestCode)
	w.dateTime(m.Time)
	return w.bytesOut()
}

func DecodeOperationRequest(body []byte) (OperationRequest, error) {
	r := newByteReader(body)
	m := OperationRequest{RouteNumber: r.u32(), EmployeeID: r.cstring(), RequestCode: r.u8()}
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.Time = decodeBCDDateTime(t)
	return m, r.err
}

// Business type codes (Table 13), used in DepartureQueue.BusinessType.
const (
	BusinessUpwardRun         = 0x01
	BusinessDownwardRun       = 0x02
	BusinessCircularRun       = 0x03
	BusinessStopAtOrigin      = 0x04
	BusinessStopAtTermination = 0x05
	BusinessExitStation       = 0x80
	BusinessEntryStation      = 0x81
	BusinessRefuel            = 0x82
	BusinessInflate           = 0x83
	BusinessCharging          = 0x84
	BusinessLowRepair         = 0x85
	BusinessHighRepair        = 0x86
	BusinessMaintenance1      = 0x87
	BusinessMaintenance2      = 0x88
	BusinessMaintenance3      = 0x89
	BusinessNoPassengerStay   = 0x8A
	BusinessStopAtStation     = 0x8B
	BusinessCancelPlan        = 0x8C
)

// Schedule type codes (Table 35), used in DepartureQueue.ScheduleType.
const (
	ScheduleWholeRoad     = 0x01
	ScheduleInterRegional = 0x02
	ScheduleStayAtStation = 0x03
)

// DepartureQueue is the repeated schedule-block shape (Table 34) shared by
// OperationResponse and the driving-plan messages.
type DepartureQueue struct {
	RouteNumber              uint32
	RoadSign                 string
	TripNumber               string
	BusNumber                string
	BusinessType             byte
	ScheduleType             byte
	DriverNumber             string
	DriverName               string
	Conductor1Number         string
	Conductor2Number         string
	BeginTime                BCDDateTime
	EndTime                  BCDDateTime
	StartStationNumber       uint32
	StartStationName         string
	TerminationStationNumber uint32
	TerminationStationName   string
}

func (w *byteWriter) departureQueue(q DepartureQueue) {
	w.u32(q.RouteNumber)
	w.cstring(q.RoadSign)
	w.cstring(q.TripNumber)
	w.cstring(q.BusNumber)
	w.u8(q.BusinessType)
	w.u8(q.ScheduleType)
	w.cstring(q.DriverNumber)
	w.cstring(q.DriverName)
	w.cstring(q.Conductor1Number)
	w.cstring(q.Conductor2Number)
	w.dateTime(q.BeginTime)
	w.dateTime(q.EndTime)
	w.u32(q.StartStationNumber)
	w.cstring(q.StartStationName)
	w.u32(q.TerminationStationNumber)
	w.cstring(q.TerminationStationName)
}

func (r *byteReader) departureQueue() DepartureQueue {
	q := DepartureQueue{
		RouteNumber:      r.u32(),
		RoadSign:         r.cstring(),
		TripNumber:       r.cstring(),
		BusNumber:        r.cstring(),
		BusinessType:     r.u8(),
		ScheduleType:     r.u8(),
		DriverNumber:     r.cstring(),
		DriverName:       r.cstring(),
		Conductor1Number: r.cstring(),
		Conductor2Number: r.cstring(),
	}
	var b6 [6]byte
	copy(b6[:], r.bytesN(6))
	q.BeginTime = decodeBCDDateTime(b6)
	copy(b6[:], r.bytesN(6))
	q.EndTime = decodeBCDDateTime(b6)
	q.StartStationNumber = r.u32()
	q.StartStationName = r.cstring()
	q.TerminationStationNumber = r.u32()
	q.TerminationStationName = r.cstring()
	return q
}

const MsgIDOperationResponse = 0x8B09

// OperationResponse is message 0x8B09 (Server -> OBU).
type OperationResponse struct {
	AnsweringSerial uint16
	Agreed          bool
	RespondTime     BCDDateTime
	Queue           DepartureQueue
	AdditionalInfo  string
}

func BuildOperationResponse(m OperationResponse) ([]byte, error) {
	var w byteWriter
	w.u16(m.AnsweringSerial)
	if m.Agreed {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.dateTime(m.RespondTime)
	w.departureQueue(m.Queue)
	w.cstring(m.AdditionalInfo)
	return w.bytesOut()
}

func DecodeOperationResponse(body []byte) (OperationResponse, error) {
	r := newByteReader(body)
	m := OperationResponse{AnsweringSerial: r.u16()}
	m.Agreed = r.u8() == 1
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.RespondTime = decodeBCDDateTime(t)
	m.Queue = r.departureQueue()
	m.AdditionalInfo = r.cstring()
	return m, r.err
}

const MsgIDDrivingPlanRequest = 0x0B07

// DrivingPlanRequest is message 0x0B07 (OBU -> Server). The doc's own
// byte-offset table for this message is scrambled by PDF extraction; field
// order below follows the worked example, which is the more reliable
// signal (see package-level extraction notes).
type DrivingPlanRequest struct {
	OperatingDate BCDDateTime // date-only fields (Hour/Minute/Second unused)
	EmployeeID    string
}

func BuildDrivingPlanRequest(m DrivingPlanRequest) ([]byte, error) {
	var w byteWriter
	b, err := packBCD(dateOnlyDigits(m.OperatingDate), 3)
	if err != nil {
		return nil, err
	}
	w.raw(b)
	w.cstring(m.EmployeeID)
	return w.bytesOut()
}

func DecodeDrivingPlanRequest(body []byte) (DrivingPlanRequest, error) {
	r := newByteReader(body)
	dateBytes := r.bytesN(3)
	m := DrivingPlanRequest{EmployeeID: r.cstring()}
	if r.err == nil {
		d := decodeBCD(dateBytes)
		m.OperatingDate = BCDDateTime{Year: atoi2(d[0:2]), Month: atoi2(d[2:4]), Day: atoi2(d[4:6])}
	}
	return m, r.err
}

func dateOnlyDigits(t BCDDateTime) string {
	return fmt.Sprintf("%02d%02d%02d", t.Year, t.Month, t.Day)
}

const MsgIDDrivingPlanResponse = 0x8B07

// DrivingPlanResponse is message 0x8B07 (Server -> OBU): the day's full
// schedule for a given OBU, one DepartureQueue block per run.
type DrivingPlanResponse struct {
	OperatingDate  BCDDateTime
	RunningTimes   byte
	RoadSign       string
	StartTime      BCDDateTime
	EndTime        BCDDateTime
	Plans          []DepartureQueue
	AdditionalInfo string
}

func BuildDrivingPlanResponse(m DrivingPlanResponse) ([]byte, error) {
	var w byteWriter
	b, err := packBCD(dateOnlyDigits(m.OperatingDate), 3)
	if err != nil {
		return nil, err
	}
	w.raw(b)
	w.u8(m.RunningTimes)
	w.cstring(m.RoadSign)
	w.dateTime(m.StartTime)
	w.dateTime(m.EndTime)
	w.u8(byte(len(m.Plans)))
	for _, p := range m.Plans {
		w.departureQueue(p)
	}
	w.cstring(m.AdditionalInfo)
	return w.bytesOut()
}

func DecodeDrivingPlanResponse(body []byte) (DrivingPlanResponse, error) {
	r := newByteReader(body)
	var m DrivingPlanResponse
	dateBytes := r.bytesN(3)
	if r.err == nil {
		d := decodeBCD(dateBytes)
		m.OperatingDate = BCDDateTime{Year: atoi2(d[0:2]), Month: atoi2(d[2:4]), Day: atoi2(d[4:6])}
	}
	m.RunningTimes = r.u8()
	m.RoadSign = r.cstring()
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.StartTime = decodeBCDDateTime(t)
	copy(t[:], r.bytesN(6))
	m.EndTime = decodeBCDDateTime(t)
	count := int(r.u8())
	for i := 0; i < count && r.err == nil; i++ {
		m.Plans = append(m.Plans, r.departureQueue())
	}
	m.AdditionalInfo = r.cstring()
	return m, r.err
}
