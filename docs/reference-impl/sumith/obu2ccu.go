package sumith

// This file covers the OBU->CCU (device -> server) messages from the doc's
// GPRS-packet chapters (sections 3-34, 146-151, 157-158). Field names follow
// the doc's own abbreviations. Every Build* function returns a ready-to-send
// frame string; every Decode* function parses a Frame (see Parse/ParseVerify
// in frame.go) back into the typed struct.

// --- Login ($LGN, section 3) ---

// Login is $LGN's payload. LastLocation is the last GPS fix saved on the
// device at boot; despite reading as one logical value in the doc's prose,
// it's wire-encoded as separate flat comma fields like every other field in
// the frame (not a nested sub-string), so it's modeled here as its own
// struct rather than one opaque string.
type Login struct {
	Vehicle      string
	IMEI         string
	FirmwareVer  string
	ProtocolVer  string
	LastLocation LastKnownLocation
}

// LastKnownLocation is Login's trailing GPS-fix fields:
// "gpsfix,ddMMyy,HHmmss,lat,latdir,lng,lngdir,speed".
type LastKnownLocation struct {
	GPSFix    int
	Date      string // ddMMyy
	Time      string // HHmmss
	Latitude  float64
	LatDir    string
	Longitude float64
	LngDir    string
	SpeedKmh  float64
}

func (c *Codec) BuildLogin(m Login) string {
	loc := m.LastLocation
	return c.Build("LGN", m.Vehicle, m.IMEI, m.FirmwareVer, m.ProtocolVer,
		itoa(loc.GPSFix), loc.Date, loc.Time, ftoa(loc.Latitude), loc.LatDir, ftoa(loc.Longitude), loc.LngDir, ftoa(loc.SpeedKmh))
}

func DecodeLogin(f Frame) (Login, error) {
	r := newFieldReader(f)
	m := Login{Vehicle: r.next(), IMEI: r.next(), FirmwareVer: r.next(), ProtocolVer: r.next()}
	m.LastLocation = LastKnownLocation{
		GPSFix: r.nextInt(), Date: r.next(), Time: r.next(),
		Latitude: r.nextFloat(), LatDir: r.next(), Longitude: r.nextFloat(), LngDir: r.next(), SpeedKmh: r.nextFloat(),
	}
	return m, r.err
}

// --- Location ($LOC, section 4) ---

// Location is the periodic GPS/status report. Field order matches the doc's
// "Location Data(OBU-CCU)" table exactly (minus the vendor-ID header field,
// which the Codec's token/vendor prefix in obu2ccu callers should prepend if
// their deployment uses it — many examples fold it into the frame's own
// leading fields instead, see the doc's samples).
type Location struct {
	FirmwareVersion string
	PacketType      string // NR, EA, TA, HP, IN, IF, BD, BR, BL, BC, GO, HB, HA, PC, RT
	AlertID         int
	PacketStatus    string // L or H
	IMEI            string
	Vehicle         string
	GPSFix          int
	Date            string // ddmmyyyy
	Time            string // hhmmss (UTC)
	Latitude        float64
	LatDir          string
	Longitude       float64
	LngDir          string
	SpeedKmh        float64
	HeadingDeg      float64
	Satellites      int
	AltitudeM       float64
	PDOP            float64
	HDOP            float64
	NetworkOperator string
	Ignition        int
	MainPowerStatus int
	MainVoltage     float64
	InternalBattery float64
	EmergencyStatus int
	TamperAlert     string // C or O
	GSMSignal       int
	MCC             string
	MNC             string
	LAC             string
	CellID          string
	NMR             string
	DigitalInputs   string // 4 chars, 0/1
	DigitalOutputs  string // 2 chars, 0/1
	FrameNo         string
	OTAAddr         string // ip:port or phone number, valid on PC packets
	OTAValue        string // changed parameter value, valid on PC packets
}

func (c *Codec) BuildLocation(m Location) string {
	return c.Build("LOC",
		m.FirmwareVersion, m.PacketType, itoa(m.AlertID), m.PacketStatus, m.IMEI, m.Vehicle,
		itoa(m.GPSFix), m.Date, m.Time,
		ftoa(m.Latitude), m.LatDir, ftoa(m.Longitude), m.LngDir,
		ftoa(m.SpeedKmh), ftoa(m.HeadingDeg), itoa(m.Satellites), ftoa(m.AltitudeM), ftoa(m.PDOP), ftoa(m.HDOP),
		m.NetworkOperator, itoa(m.Ignition), itoa(m.MainPowerStatus), ftoa(m.MainVoltage), ftoa(m.InternalBattery),
		itoa(m.EmergencyStatus), m.TamperAlert, itoa(m.GSMSignal), m.MCC, m.MNC, m.LAC, m.CellID, m.NMR,
		m.DigitalInputs, m.DigitalOutputs, m.FrameNo, m.OTAAddr, m.OTAValue,
	)
}

func DecodeLocation(f Frame) (Location, error) {
	r := newFieldReader(f)
	m := Location{
		FirmwareVersion: r.next(), PacketType: r.next(), AlertID: r.nextInt(), PacketStatus: r.next(),
		IMEI: r.next(), Vehicle: r.next(), GPSFix: r.nextInt(), Date: r.next(), Time: r.next(),
		Latitude: r.nextFloat(), LatDir: r.next(), Longitude: r.nextFloat(), LngDir: r.next(),
		SpeedKmh: r.nextFloat(), HeadingDeg: r.nextFloat(), Satellites: r.nextInt(), AltitudeM: r.nextFloat(),
		PDOP: r.nextFloat(), HDOP: r.nextFloat(), NetworkOperator: r.next(), Ignition: r.nextInt(),
		MainPowerStatus: r.nextInt(), MainVoltage: r.nextFloat(), InternalBattery: r.nextFloat(),
		EmergencyStatus: r.nextInt(), TamperAlert: r.next(), GSMSignal: r.nextInt(), MCC: r.next(), MNC: r.next(),
		LAC: r.next(), CellID: r.next(), NMR: r.next(), DigitalInputs: r.next(), DigitalOutputs: r.next(),
		FrameNo: r.next(),
	}
	// OTAAddr/OTAValue are only present on PC (parameter-change) packets.
	m.OTAAddr = safeNext(r)
	m.OTAValue = safeNext(r)
	return m, r.err
}

func safeNext(r *fieldReader) string {
	if r.err != nil || r.pos >= len(r.fields) {
		return ""
	}
	return r.next()
}

// --- Health Status ($HLT, section 5) ---

type HealthStatus struct {
	OBUID                 string
	MDVRName              string
	VendorID              string
	DateTime              string // ddmmyyyyhhmmss
	PrimaryIP             string
	SecondaryIP           string
	FirmwareVersion       string
	ProtocolVersion       string
	IMEI                  string
	Storage1Status        int
	Storage1MemStatus     int
	Storage2Status        int
	Storage2MemStatus     int
	CameraStatus          [8]int
	MicrophoneStatus      [8]int
	IgnitionStatus        int
	EmergencyButton       int
	BatteryPercent        int
	LowBatteryThreshold   float64
	MemoryPercent         int
	UpdateRateIgnitionOn  int
	UpdateRateIgnitionOff int
	DigitalIOStatus       string
	AnalogIOStatus        int
}

func (c *Codec) BuildHealthStatus(m HealthStatus) string {
	fields := []string{
		m.OBUID, m.MDVRName, m.VendorID, m.DateTime, m.PrimaryIP, m.SecondaryIP, m.FirmwareVersion, m.ProtocolVersion, m.IMEI,
		itoa(m.Storage1Status), itoa(m.Storage1MemStatus), itoa(m.Storage2Status), itoa(m.Storage2MemStatus),
	}
	for _, v := range m.CameraStatus {
		fields = append(fields, itoa(v))
	}
	for _, v := range m.MicrophoneStatus {
		fields = append(fields, itoa(v))
	}
	fields = append(fields,
		itoa(m.IgnitionStatus), itoa(m.EmergencyButton), itoa(m.BatteryPercent), ftoa(m.LowBatteryThreshold),
		itoa(m.MemoryPercent), itoa(m.UpdateRateIgnitionOn), itoa(m.UpdateRateIgnitionOff), m.DigitalIOStatus, itoa(m.AnalogIOStatus),
	)
	return c.Build("HLT", fields...)
}

func DecodeHealthStatus(f Frame) (HealthStatus, error) {
	r := newFieldReader(f)
	m := HealthStatus{
		OBUID: r.next(), MDVRName: r.next(), VendorID: r.next(), DateTime: r.next(),
		PrimaryIP: r.next(), SecondaryIP: r.next(), FirmwareVersion: r.next(), ProtocolVersion: r.next(), IMEI: r.next(),
		Storage1Status: r.nextInt(), Storage1MemStatus: r.nextInt(), Storage2Status: r.nextInt(), Storage2MemStatus: r.nextInt(),
	}
	for i := range m.CameraStatus {
		m.CameraStatus[i] = r.nextInt()
	}
	for i := range m.MicrophoneStatus {
		m.MicrophoneStatus[i] = r.nextInt()
	}
	m.IgnitionStatus = r.nextInt()
	m.EmergencyButton = r.nextInt()
	m.BatteryPercent = r.nextInt()
	m.LowBatteryThreshold = r.nextFloat()
	m.MemoryPercent = r.nextInt()
	m.UpdateRateIgnitionOn = r.nextInt()
	m.UpdateRateIgnitionOff = r.nextInt()
	m.DigitalIOStatus = r.next()
	m.AnalogIOStatus = r.nextInt()
	return m, r.err
}

// --- Emergency button alert ($EPB, section 6) ---

type EmergencyAlert struct {
	IMEI        string
	PacketType  string // NM (normal) or SP (stop ack)
	DateTime    string // ddmmyyyyhhmmss
	GPSValid    string // A or V
	Latitude    float64
	LatDir      string
	Longitude   float64
	LngDir      string
	AltitudeM   float64
	SpeedKmh    float64
	Distance    float64
	Provider    string // G or N
	Vehicle     string
	ReplyNumber string
}

func (c *Codec) BuildEmergencyAlert(m EmergencyAlert) string {
	return c.Build("EPB", "EMR", m.IMEI, m.PacketType, m.DateTime, m.GPSValid,
		ftoa(m.Latitude), m.LatDir, ftoa(m.Longitude), m.LngDir, ftoa(m.AltitudeM), ftoa(m.SpeedKmh), ftoa(m.Distance),
		m.Provider, m.Vehicle, m.ReplyNumber)
}

func DecodeEmergencyAlert(f Frame) (EmergencyAlert, error) {
	r := newFieldReader(f)
	_ = r.next() // "EMR"/"SEM" sub-type marker, already implied by f.Token == "EPB"
	m := EmergencyAlert{
		IMEI: r.next(), PacketType: r.next(), DateTime: r.next(), GPSValid: r.next(),
		Latitude: r.nextFloat(), LatDir: r.next(), Longitude: r.nextFloat(), LngDir: r.next(),
		AltitudeM: r.nextFloat(), SpeedKmh: r.nextFloat(), Distance: r.nextFloat(), Provider: r.next(),
		Vehicle: r.next(), ReplyNumber: r.next(),
	}
	return m, r.err
}

// --- Generic "send Foo" responses (sections 7-14): a common shape of
// VendorID, Vehicle, IMEI, DateTime, then 1+ value fields. ---

type deviceInfoResponse struct {
	VendorID string
	Vehicle  string
	IMEI     string
	DateTime string
}

func (m deviceInfoResponse) fields() []string {
	return []string{m.VendorID, m.Vehicle, m.IMEI, m.DateTime}
}

func decodeDeviceInfoResponse(r *fieldReader) deviceInfoResponse {
	return deviceInfoResponse{VendorID: r.next(), Vehicle: r.next(), IMEI: r.next(), DateTime: r.next()}
}

// SendFirmwareVersion ($101, section 7).
type SendFirmwareVersion struct {
	deviceInfoResponse
	FirmwareVersion string
}

func (c *Codec) BuildSendFirmwareVersion(m SendFirmwareVersion) string {
	return c.Build("101", append(m.fields(), m.FirmwareVersion)...)
}
func DecodeSendFirmwareVersion(f Frame) (SendFirmwareVersion, error) {
	r := newFieldReader(f)
	m := SendFirmwareVersion{deviceInfoResponse: decodeDeviceInfoResponse(r), FirmwareVersion: r.next()}
	return m, r.err
}

// SendProtocolVersion ($102, section 8).
type SendProtocolVersion struct {
	deviceInfoResponse
	ProtocolVersion string
}

func (c *Codec) BuildSendProtocolVersion(m SendProtocolVersion) string {
	return c.Build("102", append(m.fields(), m.ProtocolVersion)...)
}
func DecodeSendProtocolVersion(f Frame) (SendProtocolVersion, error) {
	r := newFieldReader(f)
	m := SendProtocolVersion{deviceInfoResponse: decodeDeviceInfoResponse(r), ProtocolVersion: r.next()}
	return m, r.err
}

// SendMacAddress ($103, section 9).
type SendMacAddress struct {
	deviceInfoResponse
	MacAddress string
}

func (c *Codec) BuildSendMacAddress(m SendMacAddress) string {
	return c.Build("103", append(m.fields(), m.MacAddress)...)
}
func DecodeSendMacAddress(f Frame) (SendMacAddress, error) {
	r := newFieldReader(f)
	m := SendMacAddress{deviceInfoResponse: decodeDeviceInfoResponse(r), MacAddress: r.next()}
	return m, r.err
}

// SendPrimaryIP ($104, section 10). Source is the requester's server
// ip:port or phone number, echoed back per the doc.
type SendPrimaryIP struct {
	deviceInfoResponse
	Source string
	IP     string
	Port   int
}

func (c *Codec) BuildSendPrimaryIP(m SendPrimaryIP) string {
	return c.Build("104", append(m.fields(), m.Source, m.IP, itoa(m.Port))...)
}
func DecodeSendPrimaryIP(f Frame) (SendPrimaryIP, error) {
	r := newFieldReader(f)
	m := SendPrimaryIP{deviceInfoResponse: decodeDeviceInfoResponse(r), Source: r.next(), IP: r.next(), Port: r.nextInt()}
	return m, r.err
}

// SendSecondaryIP ($105, section 11).
type SendSecondaryIP struct {
	deviceInfoResponse
	Source string
	IP     string
	Port   int
}

func (c *Codec) BuildSendSecondaryIP(m SendSecondaryIP) string {
	return c.Build("105", append(m.fields(), m.Source, m.IP, itoa(m.Port))...)
}
func DecodeSendSecondaryIP(f Frame) (SendSecondaryIP, error) {
	r := newFieldReader(f)
	m := SendSecondaryIP{deviceInfoResponse: decodeDeviceInfoResponse(r), Source: r.next(), IP: r.next(), Port: r.nextInt()}
	return m, r.err
}

// SendIMEI ($106, section 12).
type SendIMEI struct {
	deviceInfoResponse
	Source string
	IMEI   string
}

func (c *Codec) BuildSendIMEI(m SendIMEI) string {
	return c.Build("106", append(m.fields(), m.Source, m.IMEI)...)
}
func DecodeSendIMEI(f Frame) (SendIMEI, error) {
	r := newFieldReader(f)
	m := SendIMEI{deviceInfoResponse: decodeDeviceInfoResponse(r), Source: r.next(), IMEI: r.next()}
	return m, r.err
}

// SendVehicleRegistrationNumber ($107, section 13).
type SendVehicleRegistrationNumber struct {
	deviceInfoResponse
	Source           string
	VehicleRegNumber string
}

func (c *Codec) BuildSendVehicleRegistrationNumber(m SendVehicleRegistrationNumber) string {
	return c.Build("107", append(m.fields(), m.Source, m.VehicleRegNumber)...)
}
func DecodeSendVehicleRegistrationNumber(f Frame) (SendVehicleRegistrationNumber, error) {
	r := newFieldReader(f)
	m := SendVehicleRegistrationNumber{deviceInfoResponse: decodeDeviceInfoResponse(r), Source: r.next(), VehicleRegNumber: r.next()}
	return m, r.err
}

// SendIgnitionMode ($108, section 14).
type SendIgnitionMode struct {
	deviceInfoResponse
	Source                    string
	EmergencyButtonMode       int
	SMSMode                   int
	EmergencyTransmitInterval int
	SleepShutdownMode         int
	SleepShutdownDelayMin     int
	SleepFrequencySec         int
	NormalFrequencySec        int
	PhoneNumber               string
}

func (c *Codec) BuildSendIgnitionMode(m SendIgnitionMode) string {
	return c.Build("108", append(m.fields(),
		m.Source, itoa(m.EmergencyButtonMode), itoa(m.SMSMode), itoa(m.EmergencyTransmitInterval),
		itoa(m.SleepShutdownMode), itoa(m.SleepShutdownDelayMin), itoa(m.SleepFrequencySec), itoa(m.NormalFrequencySec), m.PhoneNumber,
	)...)
}
func DecodeSendIgnitionMode(f Frame) (SendIgnitionMode, error) {
	r := newFieldReader(f)
	m := SendIgnitionMode{
		deviceInfoResponse: decodeDeviceInfoResponse(r), Source: r.next(),
		EmergencyButtonMode: r.nextInt(), SMSMode: r.nextInt(), EmergencyTransmitInterval: r.nextInt(),
		SleepShutdownMode: r.nextInt(), SleepShutdownDelayMin: r.nextInt(), SleepFrequencySec: r.nextInt(),
		NormalFrequencySec: r.nextInt(), PhoneNumber: r.next(),
	}
	return m, r.err
}

// --- Common ack, OBU->CCU (section 15) ---

type CommonAck struct {
	Vehicle     string
	IMEI        string
	DateTime    string
	Success     bool
	MessageType string
}

func (c *Codec) BuildCommonAck(m CommonAck) string {
	return c.Build("ACK", m.Vehicle, m.IMEI, m.DateTime, boolFlag01(m.Success), m.MessageType)
}
func DecodeCommonAck(f Frame) (CommonAck, error) {
	r := newFieldReader(f)
	m := CommonAck{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next()}
	m.Success = r.next() == "1"
	m.MessageType = r.next()
	return m, r.err
}

// --- Panic message ($002, section 16) ---

type PanicMessage struct {
	Vehicle   string
	IMEI      string
	DateTime  string
	PanicID   int // 1 terrorist,2 hijacked,3 accident,4 fire,5 drowning
	Latitude  float64
	Longitude float64
}

func (c *Codec) BuildPanicMessage(m PanicMessage) string {
	return c.Build("002", m.Vehicle, m.IMEI, m.DateTime, itoa(m.PanicID), ftoa(m.Latitude), ftoa(m.Longitude))
}
func DecodePanicMessage(f Frame) (PanicMessage, error) {
	r := newFieldReader(f)
	m := PanicMessage{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), PanicID: r.nextInt(), Latitude: r.nextFloat(), Longitude: r.nextFloat()}
	return m, r.err
}

// --- Route/duty lifecycle (sections 17-25) ---

// CurrentRouteStart ($050, section 17).
type CurrentRouteStart struct {
	Vehicle                string
	IMEI                   string
	DateTime               string
	RouteID                string
	ExpectedCompletionTime string // HHMM
	DutyID                 string
	TripID                 string
}

func (c *Codec) BuildCurrentRouteStart(m CurrentRouteStart) string {
	return c.Build("050", m.Vehicle, m.IMEI, m.DateTime, m.RouteID, m.ExpectedCompletionTime, m.DutyID, m.TripID)
}
func DecodeCurrentRouteStart(f Frame) (CurrentRouteStart, error) {
	r := newFieldReader(f)
	m := CurrentRouteStart{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), RouteID: r.next(), ExpectedCompletionTime: r.next(), DutyID: r.next(), TripID: r.next()}
	return m, r.err
}

// CancelledRoute ($054, section 18).
type CancelledRoute struct {
	Vehicle  string
	IMEI     string
	DateTime string
	RouteID  string
	DutyID   string
	TripID   string
}

func (c *Codec) BuildCancelledRoute(m CancelledRoute) string {
	return c.Build("054", m.Vehicle, m.IMEI, m.DateTime, m.RouteID, m.DutyID, m.TripID)
}
func DecodeCancelledRoute(f Frame) (CancelledRoute, error) {
	r := newFieldReader(f)
	m := CancelledRoute{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), RouteID: r.next(), DutyID: r.next(), TripID: r.next()}
	return m, r.err
}

// stopEvent is the shared shape of ApproachingStop/ReachStop/DepartStop/
// SkipStop (sections 19-22): Vehicle, IMEI, DateTime, DutyID, RouteID,
// StopID, Reserved, TripID.
type stopEvent struct {
	Vehicle  string
	IMEI     string
	DateTime string
	DutyID   string
	RouteID  string
	StopID   string
	Reserved string
	TripID   string
}

func (m stopEvent) fields() []string {
	return []string{m.Vehicle, m.IMEI, m.DateTime, m.DutyID, m.RouteID, m.StopID, m.Reserved, m.TripID}
}
func decodeStopEvent(r *fieldReader) stopEvent {
	return stopEvent{
		Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), DutyID: r.next(),
		RouteID: r.next(), StopID: r.next(), Reserved: r.next(), TripID: r.next(),
	}
}

type ApproachingStop struct{ stopEvent }
type ReachStop struct{ stopEvent }
type DepartStop struct{ stopEvent }
type SkipStop struct{ stopEvent }

func (c *Codec) BuildApproachingStop(m ApproachingStop) string { return c.Build("062", m.fields()...) }
func (c *Codec) BuildReachStop(m ReachStop) string             { return c.Build("063", m.fields()...) }
func (c *Codec) BuildDepartStop(m DepartStop) string           { return c.Build("064", m.fields()...) }
func (c *Codec) BuildSkipStop(m SkipStop) string               { return c.Build("065", m.fields()...) }

func DecodeApproachingStop(f Frame) (ApproachingStop, error) {
	r := newFieldReader(f)
	return ApproachingStop{decodeStopEvent(r)}, r.err
}
func DecodeReachStop(f Frame) (ReachStop, error) {
	r := newFieldReader(f)
	return ReachStop{decodeStopEvent(r)}, r.err
}
func DecodeDepartStop(f Frame) (DepartStop, error) {
	r := newFieldReader(f)
	return DepartStop{decodeStopEvent(r)}, r.err
}
func DecodeSkipStop(f Frame) (SkipStop, error) {
	r := newFieldReader(f)
	return SkipStop{decodeStopEvent(r)}, r.err
}

// RouteEnd ($066, section 23).
type RouteEnd struct {
	Vehicle  string
	IMEI     string
	DateTime string
	DutyID   string
	RouteID  string
	TripID   string
}

func (c *Codec) BuildRouteEnd(m RouteEnd) string {
	return c.Build("066", m.Vehicle, m.IMEI, m.DateTime, m.DutyID, m.RouteID, m.TripID)
}
func DecodeRouteEnd(f Frame) (RouteEnd, error) {
	r := newFieldReader(f)
	m := RouteEnd{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), DutyID: r.next(), RouteID: r.next(), TripID: r.next()}
	return m, r.err
}

// dutyEvent is the shared shape of DutyStart/DutyEnd (sections 24-25).
type dutyEvent struct {
	Vehicle  string
	IMEI     string
	DateTime string
	DutyID   string
	Reserved string
}

func (m dutyEvent) fields() []string {
	return []string{m.Vehicle, m.IMEI, m.DateTime, m.DutyID, m.Reserved}
}
func decodeDutyEvent(r *fieldReader) dutyEvent {
	return dutyEvent{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), DutyID: r.next(), Reserved: r.next()}
}

type DutyStart struct{ dutyEvent }
type DutyEnd struct{ dutyEvent }

func (c *Codec) BuildDutyStart(m DutyStart) string { return c.Build("067", m.fields()...) }
func (c *Codec) BuildDutyEnd(m DutyEnd) string     { return c.Build("068", m.fields()...) }
func DecodeDutyStart(f Frame) (DutyStart, error) {
	r := newFieldReader(f)
	return DutyStart{decodeDutyEvent(r)}, r.err
}
func DecodeDutyEnd(f Frame) (DutyEnd, error) {
	r := newFieldReader(f)
	return DutyEnd{decodeDutyEvent(r)}, r.err
}

// --- Over speed / stoppage (sections 26-28) ---

// OverSpeedStart ($071, section 26).
type OverSpeedStart struct {
	Vehicle      string
	IMEI         string
	DateTime     string
	UpperLimit   float64
	CurrentSpeed float64
	Latitude     float64
	Longitude    float64
}

func (c *Codec) BuildOverSpeedStart(m OverSpeedStart) string {
	return c.Build("071", m.Vehicle, m.IMEI, m.DateTime, ftoa(m.UpperLimit), ftoa(m.CurrentSpeed), ftoa(m.Latitude), ftoa(m.Longitude))
}
func DecodeOverSpeedStart(f Frame) (OverSpeedStart, error) {
	r := newFieldReader(f)
	m := OverSpeedStart{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), UpperLimit: r.nextFloat(), CurrentSpeed: r.nextFloat(), Latitude: r.nextFloat(), Longitude: r.nextFloat()}
	return m, r.err
}

// OverSpeedEnd ($072, section 27).
type OverSpeedEnd struct {
	Vehicle      string
	IMEI         string
	DateTime     string
	UpperLimit   float64
	CurrentSpeed float64
	MaxSpeed     float64
	DurationSec  int
	Latitude     float64
	Longitude    float64
}

func (c *Codec) BuildOverSpeedEnd(m OverSpeedEnd) string {
	return c.Build("072", m.Vehicle, m.IMEI, m.DateTime, ftoa(m.UpperLimit), ftoa(m.CurrentSpeed), ftoa(m.MaxSpeed), itoa(m.DurationSec), ftoa(m.Latitude), ftoa(m.Longitude))
}
func DecodeOverSpeedEnd(f Frame) (OverSpeedEnd, error) {
	r := newFieldReader(f)
	m := OverSpeedEnd{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), UpperLimit: r.nextFloat(), CurrentSpeed: r.nextFloat(), MaxSpeed: r.nextFloat(), DurationSec: r.nextInt(), Latitude: r.nextFloat(), Longitude: r.nextFloat()}
	return m, r.err
}

// VehicleStoppage ($073, section 28).
type VehicleStoppage struct {
	Vehicle      string
	IMEI         string
	DateTime     string
	ThresholdMin int
	DurationMin  int
	Latitude     float64
	Longitude    float64
}

func (c *Codec) BuildVehicleStoppage(m VehicleStoppage) string {
	return c.Build("073", m.Vehicle, m.IMEI, m.DateTime, itoa(m.ThresholdMin), itoa(m.DurationMin), ftoa(m.Latitude), ftoa(m.Longitude))
}
func DecodeVehicleStoppage(f Frame) (VehicleStoppage, error) {
	r := newFieldReader(f)
	m := VehicleStoppage{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), ThresholdMin: r.nextInt(), DurationMin: r.nextInt(), Latitude: r.nextFloat(), Longitude: r.nextFloat()}
	return m, r.err
}

// --- Reset ack, Driver halt, Schedule request, Special msg, Ping, OBU
// started (sections 29-34) ---

type ResetAck struct {
	Vehicle  string
	IMEI     string
	DateTime string
}

func (c *Codec) BuildResetAck(m ResetAck) string {
	return c.Build("081", m.Vehicle, m.IMEI, m.DateTime, "")
}
func DecodeResetAck(f Frame) (ResetAck, error) {
	r := newFieldReader(f)
	m := ResetAck{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next()}
	return m, r.err
}

type DriverHaltJustification struct {
	Vehicle         string
	IMEI            string
	DateTime        string
	JustificationID int
}

func (c *Codec) BuildDriverHaltJustification(m DriverHaltJustification) string {
	return c.Build("084", m.Vehicle, m.IMEI, m.DateTime, itoa(m.JustificationID))
}
func DecodeDriverHaltJustification(f Frame) (DriverHaltJustification, error) {
	r := newFieldReader(f)
	m := DriverHaltJustification{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), JustificationID: r.nextInt()}
	return m, r.err
}

type ScheduleRequest struct {
	Vehicle  string
	IMEI     string
	DateTime string
	DutyID   string
}

func (c *Codec) BuildScheduleRequest(m ScheduleRequest) string {
	return c.Build("085", m.Vehicle, m.IMEI, m.DateTime, m.DutyID)
}
func DecodeScheduleRequest(f Frame) (ScheduleRequest, error) {
	r := newFieldReader(f)
	m := ScheduleRequest{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), DutyID: r.next()}
	return m, r.err
}

type SpecialMessage struct {
	Vehicle   string
	IMEI      string
	DateTime  string
	MessageID int // 1 special-on-route,2 breakdown,3 trial,4 off-route,5 feeder-to-BRTS
}

func (c *Codec) BuildSpecialMessage(m SpecialMessage) string {
	return c.Build("086", m.Vehicle, m.IMEI, m.DateTime, itoa(m.MessageID))
}
func DecodeSpecialMessage(f Frame) (SpecialMessage, error) {
	r := newFieldReader(f)
	m := SpecialMessage{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), MessageID: r.nextInt()}
	return m, r.err
}

type PingCheck struct {
	Vehicle  string
	IMEI     string
	DateTime string
	Data     string
}

func (c *Codec) BuildPingCheck(m PingCheck) string {
	return c.Build("090", m.Vehicle, m.IMEI, m.DateTime, m.Data)
}
func DecodePingCheck(f Frame) (PingCheck, error) {
	r := newFieldReader(f)
	m := PingCheck{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), Data: r.next()}
	return m, r.err
}

type OBUStarted struct {
	Vehicle  string
	IMEI     string
	DateTime string
	Reason   int // 0 normal,1 watchdog,2 low-power
}

func (c *Codec) BuildOBUStarted(m OBUStarted) string {
	return c.Build("094", m.Vehicle, m.IMEI, m.DateTime, itoa(m.Reason))
}
func DecodeOBUStarted(f Frame) (OBUStarted, error) {
	r := newFieldReader(f)
	m := OBUStarted{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), Reason: r.nextInt()}
	return m, r.err
}

// --- CAN data (section 146) ---

// CANStream is one CAN1/CAN2-style parameter block within a CANData report.
// The doc's field list is per-vehicle-model (odometer, SoC, temperatures,
// gear, doors, ...); Values holds them in wire order so callers can adapt to
// whichever variant their fleet uses without a rigid 20-field struct.
type CANStream struct {
	Label  string // e.g. "CAN1:" or "CAN2:"
	Values []string
}

type CANData struct {
	Vehicle string
	IMEI    string
	Date    string // ddmmyyyy
	Time    string // HHmmss
	Streams []CANStream
}

func (c *Codec) BuildCANData(m CANData) string {
	fields := []string{m.Vehicle, m.IMEI, m.Date, m.Time}
	for _, s := range m.Streams {
		fields = append(fields, s.Label)
		fields = append(fields, s.Values...)
	}
	return c.Build("014", fields...)
}

// DecodeCANData splits the remaining fields into streams at each "CANx:"
// label boundary.
func DecodeCANData(f Frame) (CANData, error) {
	r := newFieldReader(f)
	m := CANData{Vehicle: r.next(), IMEI: r.next(), Date: r.next(), Time: r.next()}
	if r.err != nil {
		return m, r.err
	}
	rest := r.rest()
	var cur *CANStream
	for _, v := range rest {
		if len(v) > 0 && v[len(v)-1] == ':' {
			m.Streams = append(m.Streams, CANStream{Label: v})
			cur = &m.Streams[len(m.Streams)-1]
			continue
		}
		if cur != nil {
			cur.Values = append(cur.Values, v)
		}
	}
	return m, nil
}

// --- APC (passenger counting), sections 147-148, 158 ---

// APCOnEveryStop ($009, section 147).
type APCOnEveryStop struct {
	Vehicle      string
	PacketStatus string // L or H
	IMEI         string
	DateTime     string
	Latitude     float64
	LatDir       string
	Longitude    float64
	LngDir       string
	StopName     string
	RouteNumber  string
	FrontIn      int
	FrontOut     int
	BackIn       int
	BackOut      int
}

func (c *Codec) BuildAPCOnEveryStop(m APCOnEveryStop) string {
	return c.Build("009", m.Vehicle, m.PacketStatus, m.IMEI, m.DateTime, ftoa(m.Latitude), m.LatDir, ftoa(m.Longitude), m.LngDir,
		m.StopName, m.RouteNumber, itoa(m.FrontIn), itoa(m.FrontOut), itoa(m.BackIn), itoa(m.BackOut))
}
func DecodeAPCOnEveryStop(f Frame) (APCOnEveryStop, error) {
	r := newFieldReader(f)
	m := APCOnEveryStop{
		Vehicle: r.next(), PacketStatus: r.next(), IMEI: r.next(), DateTime: r.next(),
		Latitude: r.nextFloat(), LatDir: r.next(), Longitude: r.nextFloat(), LngDir: r.next(),
		StopName: r.next(), RouteNumber: r.next(), FrontIn: r.nextInt(), FrontOut: r.nextInt(), BackIn: r.nextInt(), BackOut: r.nextInt(),
	}
	return m, r.err
}

// APCOnRouteEnd ($012, section 148).
type APCOnRouteEnd struct {
	Vehicle      string
	PacketStatus string
	IMEI         string
	Date         string // yymmdd
	StartTime    string // HHMMSS
	EndTime      string
	RouteName    string
	RouteNumber  string
	FrontIn      int
	FrontOut     int
	BackIn       int
	BackOut      int
}

func (c *Codec) BuildAPCOnRouteEnd(m APCOnRouteEnd) string {
	return c.Build("012", m.Vehicle, m.PacketStatus, m.IMEI, m.Date, m.StartTime, m.EndTime, m.RouteName, m.RouteNumber,
		itoa(m.FrontIn), itoa(m.FrontOut), itoa(m.BackIn), itoa(m.BackOut))
}
func DecodeAPCOnRouteEnd(f Frame) (APCOnRouteEnd, error) {
	r := newFieldReader(f)
	m := APCOnRouteEnd{
		Vehicle: r.next(), PacketStatus: r.next(), IMEI: r.next(), Date: r.next(), StartTime: r.next(), EndTime: r.next(),
		RouteName: r.next(), RouteNumber: r.next(), FrontIn: r.nextInt(), FrontOut: r.nextInt(), BackIn: r.nextInt(), BackOut: r.nextInt(),
	}
	return m, r.err
}

// APCOnDayEnd ($013, section 158).
type APCOnDayEnd struct {
	Vehicle      string
	PacketStatus string
	IMEI         string
	Date         string
	EndTime      string
	TotalIn      int
	TotalOut     int
}

func (c *Codec) BuildAPCOnDayEnd(m APCOnDayEnd) string {
	return c.Build("013", m.Vehicle, m.PacketStatus, m.IMEI, m.Date, m.EndTime, itoa(m.TotalIn), itoa(m.TotalOut))
}
func DecodeAPCOnDayEnd(f Frame) (APCOnDayEnd, error) {
	r := newFieldReader(f)
	m := APCOnDayEnd{Vehicle: r.next(), PacketStatus: r.next(), IMEI: r.next(), Date: r.next(), EndTime: r.next(), TotalIn: r.nextInt(), TotalOut: r.nextInt()}
	return m, r.err
}

// --- Conductor duty (sections 149-150) ---

type conductorDutyEvent struct {
	Vehicle  string
	IMEI     string
	DateTime string
	DutyID   string
	Reserved string
}

func (m conductorDutyEvent) fields() []string {
	return []string{m.Vehicle, m.IMEI, m.DateTime, m.DutyID, m.Reserved}
}
func decodeConductorDutyEvent(r *fieldReader) conductorDutyEvent {
	return conductorDutyEvent{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next(), DutyID: r.next(), Reserved: r.next()}
}

type ConductorDutyStart struct{ conductorDutyEvent }
type ConductorDutyEnd struct{ conductorDutyEvent }

// Note: the doc's own field tables list "147" as the header for both start
// and end packets (apparent copy-paste error), but the worked example for
// the end packet uses "$148,...#" — this implementation follows the worked
// example, which is the more reliable signal.
func (c *Codec) BuildConductorDutyStart(m ConductorDutyStart) string {
	return c.Build("147", m.fields()...)
}
func (c *Codec) BuildConductorDutyEnd(m ConductorDutyEnd) string {
	return c.Build("148", m.fields()...)
}

func DecodeConductorDutyStart(f Frame) (ConductorDutyStart, error) {
	r := newFieldReader(f)
	return ConductorDutyStart{decodeConductorDutyEvent(r)}, r.err
}
func DecodeConductorDutyEnd(f Frame) (ConductorDutyEnd, error) {
	r := newFieldReader(f)
	return ConductorDutyEnd{decodeConductorDutyEvent(r)}, r.err
}

// --- ALCOBRAKE (section 159) ---

type AlcoBrakeResult struct {
	Vehicle  string
	IMEI     string
	DateTime string
	Passed   bool
}

func (c *Codec) BuildAlcoBrakeResult(m AlcoBrakeResult) string {
	return c.Build("015", m.Vehicle, m.IMEI, m.DateTime, boolFlag01(m.Passed))
}
func DecodeAlcoBrakeResult(f Frame) (AlcoBrakeResult, error) {
	r := newFieldReader(f)
	m := AlcoBrakeResult{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next()}
	m.Passed = r.next() == "1"
	return m, r.err
}
