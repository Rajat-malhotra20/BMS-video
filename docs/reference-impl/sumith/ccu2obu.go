package sumith

// This file covers the CCU->OBU (server -> device) commands from sections
// 72-100 and 130-146. Camera configuration commands (sections 45-71 in the
// numbering table, headers 101-127) share one shape — VendorID, Vehicle,
// IMEI, DateTime, a channel number, one value — so instead of ~26
// near-duplicate structs they're built on a single generic constructor;
// each documented command still gets its own named Go function for
// type-safe call sites.

// deviceCmdPrefix is the Vehicle/IMEI/DateTime prefix shared by nearly every
// CCU->OBU command.
type deviceCmdPrefix struct {
	Vehicle  string
	IMEI     string
	DateTime string
}

func (p deviceCmdPrefix) fields() []string { return []string{p.Vehicle, p.IMEI, p.DateTime} }
func decodeDeviceCmdPrefix(r *fieldReader) deviceCmdPrefix {
	return deviceCmdPrefix{Vehicle: r.next(), IMEI: r.next(), DateTime: r.next()}
}

// CameraChannelValueCmd is the common shape of every single-value camera
// configuration command (sections covering resolution/quality/bitrate/
// bitrate-type/max-bitrate/frame-rate/frame-interval, for both main and sub
// stream, plus several other single-int camera settings).
type CameraChannelValueCmd struct {
	deviceCmdPrefix
	Channel int
	Value   int
}

func (c *Codec) buildCameraChannelValueCmd(token string, m CameraChannelValueCmd) string {
	return c.Build(token, append(m.fields(), itoa(m.Channel), itoa(m.Value))...)
}

func decodeCameraChannelValueCmd(f Frame) (CameraChannelValueCmd, error) {
	r := newFieldReader(f)
	m := CameraChannelValueCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Channel: r.nextInt(), Value: r.nextInt()}
	return m, r.err
}

// Camera config command tokens (headers 101-127) and their meaning. Use
// with Build/DecodeCameraChannelValueCmd.
const (
	TokMainStreamResolution    = "101"
	TokMainStreamQuality       = "102"
	TokMainStreamBitrate       = "103"
	TokMainStreamBitrateType   = "104"
	TokMainStreamMaxBitrate    = "105"
	TokMainStreamFrameRate     = "106"
	TokMainStreamFrameInterval = "107"
	TokSubStreamResolution     = "108"
	TokSubStreamQuality        = "109"
	TokSubStreamBitrate        = "110"
	TokSubStreamBitrateType    = "111"
	TokSubStreamMaxBitrate     = "112"
	TokSubStreamFrameRate      = "113"
	TokSubStreamFrameInterval  = "114"
	TokVideoRecordingEnable    = "115"
	TokAudioRecordingEnable    = "116"
	TokSnapshotResolution      = "126"
	TokSnapshotFrequency       = "127"
)

func (c *Codec) BuildMainStreamResolution(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamResolution, m)
}
func (c *Codec) BuildMainStreamQuality(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamQuality, m)
}
func (c *Codec) BuildMainStreamBitrate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamBitrate, m)
}
func (c *Codec) BuildMainStreamBitrateType(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamBitrateType, m)
}
func (c *Codec) BuildMainStreamMaxBitrate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamMaxBitrate, m)
}
func (c *Codec) BuildMainStreamFrameRate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamFrameRate, m)
}
func (c *Codec) BuildMainStreamFrameInterval(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokMainStreamFrameInterval, m)
}
func (c *Codec) BuildSubStreamResolution(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamResolution, m)
}
func (c *Codec) BuildSubStreamQuality(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamQuality, m)
}
func (c *Codec) BuildSubStreamBitrate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamBitrate, m)
}
func (c *Codec) BuildSubStreamBitrateType(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamBitrateType, m)
}
func (c *Codec) BuildSubStreamMaxBitrate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamMaxBitrate, m)
}
func (c *Codec) BuildSubStreamFrameRate(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamFrameRate, m)
}
func (c *Codec) BuildSubStreamFrameInterval(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSubStreamFrameInterval, m)
}
func (c *Codec) BuildVideoRecordingEnable(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokVideoRecordingEnable, m)
} // Value: 1 enable, 0 disable
func (c *Codec) BuildAudioRecordingEnable(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokAudioRecordingEnable, m)
}
func (c *Codec) BuildSnapshotResolution(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSnapshotResolution, m)
} // Value: 0 sub,1 main
func (c *Codec) BuildSnapshotFrequency(m CameraChannelValueCmd) string {
	return c.buildCameraChannelValueCmd(TokSnapshotFrequency, m)
} // Value: minutes

// DecodeCameraChannelValueCmd decodes any of the above (the frame's own
// Token tells you which command it was).
func DecodeCameraChannelValueCmd(f Frame) (CameraChannelValueCmd, error) {
	return decodeCameraChannelValueCmd(f)
}

// --- Camera name (string value instead of int), section 61 ($117) ---

type CameraNameCmd struct {
	deviceCmdPrefix
	Channel int
	Name    string
}

func (c *Codec) BuildCameraName(m CameraNameCmd) string {
	return c.Build("117", append(m.fields(), itoa(m.Channel), m.Name)...)
}
func DecodeCameraName(f Frame) (CameraNameCmd, error) {
	r := newFieldReader(f)
	m := CameraNameCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Channel: r.nextInt(), Name: r.next()}
	return m, r.err
}

// --- Recording schedule mode ($118) and schedule ($119) ---

// RecordingModeCmd toggles continuous vs schedule-based recording.
type RecordingModeCmd struct {
	deviceCmdPrefix
	ScheduleBased bool // false: continuous, true: schedule-based
}

func (c *Codec) BuildRecordingMode(m RecordingModeCmd) string {
	return c.Build("118", append(m.fields(), boolFlag01(m.ScheduleBased))...)
}
func DecodeRecordingMode(f Frame) (RecordingModeCmd, error) {
	r := newFieldReader(f)
	m := RecordingModeCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	m.ScheduleBased = r.next() == "1"
	return m, r.err
}

// RecordingScheduleCmd sets the weekly recording window.
type RecordingScheduleCmd struct {
	deviceCmdPrefix
	Days      [7]bool // Sunday..Saturday
	StartTime string  // HHmm
	EndTime   string  // HHmm
}

func (c *Codec) BuildRecordingSchedule(m RecordingScheduleCmd) string {
	fields := m.fields()
	for _, d := range m.Days {
		fields = append(fields, boolFlag01(d))
	}
	fields = append(fields, m.StartTime, m.EndTime)
	return c.Build("119", fields...)
}
func DecodeRecordingSchedule(f Frame) (RecordingScheduleCmd, error) {
	r := newFieldReader(f)
	m := RecordingScheduleCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	for i := range m.Days {
		m.Days[i] = r.next() == "1"
	}
	m.StartTime = r.next()
	m.EndTime = r.next()
	return m, r.err
}

// --- Video expiry ($120), motion detection enable/sensitivity/area
// ($121-123), pre/post-event record time ($124-125) ---

type VideoExpiryCmd struct {
	deviceCmdPrefix
	Days int // 0 = overwrite cyclically
}

func (c *Codec) BuildVideoExpiry(m VideoExpiryCmd) string {
	return c.Build("120", append(m.fields(), itoa(m.Days))...)
}
func DecodeVideoExpiry(f Frame) (VideoExpiryCmd, error) {
	r := newFieldReader(f)
	m := VideoExpiryCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Days: r.nextInt()}
	return m, r.err
}

type MotionDetectEnableCmd struct {
	deviceCmdPrefix
	Channel       int
	Enable        bool
	RecordTimeMin int
}

func (c *Codec) BuildMotionDetectEnable(m MotionDetectEnableCmd) string {
	return c.Build("121", append(m.fields(), itoa(m.Channel), boolFlag01(m.Enable), itoa(m.RecordTimeMin))...)
}
func DecodeMotionDetectEnable(f Frame) (MotionDetectEnableCmd, error) {
	r := newFieldReader(f)
	m := MotionDetectEnableCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Channel: r.nextInt()}
	m.Enable = r.next() == "1"
	m.RecordTimeMin = r.nextInt()
	return m, r.err
}

type MotionDetectSensitivityCmd struct {
	deviceCmdPrefix
	Channel       int
	Sensitivity   int // 1-100
	Threshold     int // 1-100
	RecordTimeMin int
}

func (c *Codec) BuildMotionDetectSensitivity(m MotionDetectSensitivityCmd) string {
	return c.Build("122", append(m.fields(), itoa(m.Channel), itoa(m.Sensitivity), itoa(m.Threshold), itoa(m.RecordTimeMin))...)
}
func DecodeMotionDetectSensitivity(f Frame) (MotionDetectSensitivityCmd, error) {
	r := newFieldReader(f)
	m := MotionDetectSensitivityCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Channel: r.nextInt(), Sensitivity: r.nextInt(), Threshold: r.nextInt(), RecordTimeMin: r.nextInt()}
	return m, r.err
}

type MotionDetectAreaCmd struct {
	deviceCmdPrefix
	Channel        int
	X1, X2, Y1, Y2 int
	RecordTimeMin  int
}

func (c *Codec) BuildMotionDetectArea(m MotionDetectAreaCmd) string {
	return c.Build("123", append(m.fields(), itoa(m.Channel), itoa(m.X1), itoa(m.X2), itoa(m.Y1), itoa(m.Y2), itoa(m.RecordTimeMin))...)
}
func DecodeMotionDetectArea(f Frame) (MotionDetectAreaCmd, error) {
	r := newFieldReader(f)
	m := MotionDetectAreaCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Channel: r.nextInt(), X1: r.nextInt(), X2: r.nextInt(), Y1: r.nextInt(), Y2: r.nextInt(), RecordTimeMin: r.nextInt()}
	return m, r.err
}

// PreEventRecordTime ($124) / PostEventRecordTime ($125): both are a bare
// minutes value, no channel.
type EventRecordTimeCmd struct {
	deviceCmdPrefix
	Minutes int
}

func (c *Codec) BuildPreEventRecordTime(m EventRecordTimeCmd) string {
	return c.Build("124", append(m.fields(), itoa(m.Minutes))...)
}
func (c *Codec) BuildPostEventRecordTime(m EventRecordTimeCmd) string {
	return c.Build("125", append(m.fields(), itoa(m.Minutes))...)
}
func DecodeEventRecordTime(f Frame) (EventRecordTimeCmd, error) {
	r := newFieldReader(f)
	m := EventRecordTimeCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Minutes: r.nextInt()}
	return m, r.err
}

// --- Ignition duration / device identity queries / IP+APN config
// (sections 72-97) ---

// SetIgnitionDuration ($128, section 72).
type SetIgnitionDurationCmd struct {
	deviceCmdPrefix
	SleepMode          int // 0 sleep, 1 shutdown
	DelayMin           int
	SleepFrequencySec  int
	NormalFrequencySec int
}

func (c *Codec) BuildSetIgnitionDuration(m SetIgnitionDurationCmd) string {
	return c.Build("128", append(m.fields(), itoa(m.SleepMode), itoa(m.DelayMin), itoa(m.SleepFrequencySec), itoa(m.NormalFrequencySec))...)
}
func DecodeSetIgnitionDuration(f Frame) (SetIgnitionDurationCmd, error) {
	r := newFieldReader(f)
	m := SetIgnitionDurationCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), SleepMode: r.nextInt(), DelayMin: r.nextInt(), SleepFrequencySec: r.nextInt(), NormalFrequencySec: r.nextInt()}
	return m, r.err
}

// deviceQuery is the "just ask for X" shape used by sections 73,74,75,76,77,
// 78,80,87 (Get Firmware/Protocol/Mac/VehReg/Ignition/PrimaryIP/SecondaryIP/
// IMEI): Vehicle, IMEI, DateTime, Data (always "0").
type deviceQuery struct{ deviceCmdPrefix }

func (c *Codec) buildDeviceQuery(token string, p deviceCmdPrefix) string {
	return c.Build(token, append(p.fields(), "0")...)
}
func decodeDeviceQuery(f Frame) (deviceQuery, error) {
	r := newFieldReader(f)
	m := deviceQuery{decodeDeviceCmdPrefix(r)}
	_ = r.next() // trailing "0" placeholder
	return m, r.err
}

const (
	TokGetFirmwareVersion = "129"
	TokGetProtocolVersion = "130"
	TokGetMacAddress      = "131"
	TokGetVehicleRegNum   = "143"
	TokGetIgnitionMode    = "144"
	TokGetPrimaryIP       = "132"
	TokGetSecondaryIP     = "134"
	TokGetIMEI            = "141"
	TokGetAPN             = "145"
)

func (c *Codec) BuildGetFirmwareVersion(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetFirmwareVersion, p)
}
func (c *Codec) BuildGetProtocolVersion(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetProtocolVersion, p)
}
func (c *Codec) BuildGetMacAddress(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetMacAddress, p)
}
func (c *Codec) BuildGetVehicleRegNum(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetVehicleRegNum, p)
}
func (c *Codec) BuildGetIgnitionMode(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetIgnitionMode, p)
}
func (c *Codec) BuildGetPrimaryIP(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetPrimaryIP, p)
}
func (c *Codec) BuildGetSecondaryIP(p deviceCmdPrefix) string {
	return c.buildDeviceQuery(TokGetSecondaryIP, p)
}
func (c *Codec) BuildGetIMEI(p deviceCmdPrefix) string { return c.buildDeviceQuery(TokGetIMEI, p) }
func (c *Codec) BuildGetAPN(p deviceCmdPrefix) string  { return c.buildDeviceQuery(TokGetAPN, p) }

// SetIPCmd sets primary ($133) or secondary ($135) server IP.
type SetIPCmd struct {
	deviceCmdPrefix
	IP string
}

// NewSetIPCmd builds a SetIPCmd for use with BuildSetPrimaryIP/
// BuildSetSecondaryIP. It's the only way to construct one from outside this
// package, since deviceCmdPrefix's name is unexported. dateTime should be
// in the doc's CCU->OBU format, YYMMDDHHMMSS (see FormatDateTime).
func NewSetIPCmd(vehicle, imei, dateTime, ip string) SetIPCmd {
	return SetIPCmd{deviceCmdPrefix{Vehicle: vehicle, IMEI: imei, DateTime: dateTime}, ip}
}

func (c *Codec) BuildSetPrimaryIP(m SetIPCmd) string {
	return c.Build("133", append(m.fields(), m.IP)...)
}
func (c *Codec) BuildSetSecondaryIP(m SetIPCmd) string {
	return c.Build("135", append(m.fields(), m.IP)...)
}
func DecodeSetIP(f Frame) (SetIPCmd, error) {
	r := newFieldReader(f)
	m := SetIPCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), IP: r.next()}
	return m, r.err
}

// SetPortCmd sets the primary ($136) or secondary ($137) IP's port.
type SetPortCmd struct {
	deviceCmdPrefix
	Port int
}

// NewSetPortCmd builds a SetPortCmd for use with BuildSetPrimaryPort/
// BuildSetSecondaryPort. dateTime should be in the doc's CCU->OBU format,
// YYMMDDHHMMSS (see FormatDateTime).
func NewSetPortCmd(vehicle, imei, dateTime string, port int) SetPortCmd {
	return SetPortCmd{deviceCmdPrefix{Vehicle: vehicle, IMEI: imei, DateTime: dateTime}, port}
}

func (c *Codec) BuildSetPrimaryPort(m SetPortCmd) string {
	return c.Build("136", append(m.fields(), itoa(m.Port))...)
}
func (c *Codec) BuildSetSecondaryPort(m SetPortCmd) string {
	return c.Build("137", append(m.fields(), itoa(m.Port))...)
}
func DecodeSetPort(f Frame) (SetPortCmd, error) {
	r := newFieldReader(f)
	m := SetPortCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Port: r.nextInt()}
	return m, r.err
}

// SetVehicleRegNumCmd ($138).
type SetVehicleRegNumCmd struct {
	deviceCmdPrefix
	VehicleRegNumber string
}

func (c *Codec) BuildSetVehicleRegNum(m SetVehicleRegNumCmd) string {
	return c.Build("138", append(m.fields(), m.VehicleRegNumber)...)
}
func DecodeSetVehicleRegNum(f Frame) (SetVehicleRegNumCmd, error) {
	r := newFieldReader(f)
	m := SetVehicleRegNumCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), VehicleRegNumber: r.next()}
	return m, r.err
}

// SetAPNCmd ($139).
type SetAPNCmd struct {
	deviceCmdPrefix
	APN string
}

func (c *Codec) BuildSetAPN(m SetAPNCmd) string { return c.Build("139", append(m.fields(), m.APN)...) }
func DecodeSetAPN(f Frame) (SetAPNCmd, error) {
	r := newFieldReader(f)
	m := SetAPNCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), APN: r.next()}
	return m, r.err
}

// SetEmergencyDurationCmd ($140).
type SetEmergencyDurationCmd struct {
	deviceCmdPrefix
	Seconds int
}

func (c *Codec) BuildSetEmergencyDuration(m SetEmergencyDurationCmd) string {
	return c.Build("140", append(m.fields(), itoa(m.Seconds))...)
}
func DecodeSetEmergencyDuration(f Frame) (SetEmergencyDurationCmd, error) {
	r := newFieldReader(f)
	m := SetEmergencyDurationCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Seconds: r.nextInt()}
	return m, r.err
}

// TransmissionModeAlwaysOnCmd ($142).
type TransmissionModeAlwaysOnCmd struct {
	deviceCmdPrefix
	EmergencyButtonMode int
	SMSMode             int
	PhoneNumber         string
}

func (c *Codec) BuildTransmissionModeAlwaysOn(m TransmissionModeAlwaysOnCmd) string {
	return c.Build("142", append(m.fields(), itoa(m.EmergencyButtonMode), itoa(m.SMSMode), m.PhoneNumber)...)
}
func DecodeTransmissionModeAlwaysOn(f Frame) (TransmissionModeAlwaysOnCmd, error) {
	r := newFieldReader(f)
	m := TransmissionModeAlwaysOnCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), EmergencyButtonMode: r.nextInt(), SMSMode: r.nextInt(), PhoneNumber: r.next()}
	return m, r.err
}

// --- System reset ($063 CCU->OBU direction, section 89) ---

type SystemResetCmd struct{ deviceCmdPrefix }

func (c *Codec) BuildSystemReset(p deviceCmdPrefix) string { return c.Build("063", p.fields()...) }

// --- HARSH/OVERSPEED thresholds: SET (sections 134-137) and GET (138-141),
// both CCU->OBU, plus OBU->CCU SEND (142-145) share value shape. ---

// ThresholdCmd is the shared shape of every HARSH_ACCELERATION/HARSH_BREAK/
// RASH_TURN/OVER_SPEED get/set/send command: prefix + one int value.
type ThresholdCmd struct {
	deviceCmdPrefix
	Value int
}

func (c *Codec) buildThresholdCmd(token string, m ThresholdCmd) string {
	return c.Build(token, append(m.fields(), itoa(m.Value))...)
}
func decodeThresholdCmd(f Frame) (ThresholdCmd, error) {
	r := newFieldReader(f)
	m := ThresholdCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Value: r.nextInt()}
	return m, r.err
}

const (
	TokSetHarshAcceleration = "079"
	TokSetHarshBreak        = "080"
	TokSetRashTurn          = "082"
	TokSetOverSpeed         = "074"
	TokGetHarshAcceleration = "076"
	TokGetHarshBreak        = "077"
	TokGetRashTurn          = "078"
	TokGetOverSpeed         = "075"
)

func (c *Codec) BuildSetHarshAcceleration(m ThresholdCmd) string {
	return c.buildThresholdCmd(TokSetHarshAcceleration, m)
}
func (c *Codec) BuildSetHarshBreak(m ThresholdCmd) string {
	return c.buildThresholdCmd(TokSetHarshBreak, m)
}
func (c *Codec) BuildSetRashTurn(m ThresholdCmd) string {
	return c.buildThresholdCmd(TokSetRashTurn, m)
}
func (c *Codec) BuildSetOverSpeed(m ThresholdCmd) string {
	return c.buildThresholdCmd(TokSetOverSpeed, m)
}
func (c *Codec) BuildGetHarshAcceleration(p deviceCmdPrefix) string {
	return c.Build(TokGetHarshAcceleration, p.fields()...)
}
func (c *Codec) BuildGetHarshBreak(p deviceCmdPrefix) string {
	return c.Build(TokGetHarshBreak, p.fields()...)
}
func (c *Codec) BuildGetRashTurn(p deviceCmdPrefix) string {
	return c.Build(TokGetRashTurn, p.fields()...)
}
func (c *Codec) BuildGetOverSpeed(p deviceCmdPrefix) string {
	return c.Build(TokGetOverSpeed, p.fields()...)
}

func DecodeThresholdCmd(f Frame) (ThresholdCmd, error) { return decodeThresholdCmd(f) }

// --- System reset (CCU to OBU, section 89 in the "CCU to OBU" numbering)
// and Panic Messages (section 92) ---

type PanicMessageCmd struct {
	deviceCmdPrefix
	MessageID int
}

func (c *Codec) BuildPanicMessageCmd(m PanicMessageCmd) string {
	return c.Build("003", append(m.fields(), itoa(m.MessageID))...)
}
func DecodePanicMessageCmd(f Frame) (PanicMessageCmd, error) {
	r := newFieldReader(f)
	m := PanicMessageCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), MessageID: r.nextInt()}
	return m, r.err
}

// AdHocMessageCmd sends free-text to the device's display/TTS ($052).
type AdHocMessageCmd struct {
	deviceCmdPrefix
	Text string // max 50 bytes
}

func (c *Codec) BuildAdHocMessage(m AdHocMessageCmd) string {
	return c.Build("052", append(m.fields(), m.Text)...)
}
func DecodeAdHocMessage(f Frame) (AdHocMessageCmd, error) {
	r := newFieldReader(f)
	m := AdHocMessageCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), Text: r.next()}
	return m, r.err
}

// --- File download ($060), Schedule request response ($064), Route start
// ($051)/end ($050), Request health packet ($065), Snapshot upload ($070)
// (CCU->OBU direction) ---

type FileDownloadCmd struct {
	deviceCmdPrefix
	IsFolder bool
	Path     string
}

func (c *Codec) BuildFileDownload(m FileDownloadCmd) string {
	return c.Build("060", append(m.fields(), boolFlag01(m.IsFolder), m.Path)...)
}
func DecodeFileDownload(f Frame) (FileDownloadCmd, error) {
	r := newFieldReader(f)
	m := FileDownloadCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	m.IsFolder = r.next() == "1"
	m.Path = r.next()
	return m, r.err
}

type ScheduleRequestResponseCmd struct {
	deviceCmdPrefix
	Success bool
}

func (c *Codec) BuildScheduleRequestResponse(m ScheduleRequestResponseCmd) string {
	return c.Build("064", append(m.fields(), boolFlag01(m.Success))...)
}
func DecodeScheduleRequestResponse(f Frame) (ScheduleRequestResponseCmd, error) {
	r := newFieldReader(f)
	m := ScheduleRequestResponseCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	m.Success = r.next() == "1"
	return m, r.err
}

type RouteStartCmd struct {
	deviceCmdPrefix
	RouteID                string
	StartTime              string // YYMMDDHHMMSS
	ExpectedCompletionTime string // HHMM
	DutyID                 string
}

func (c *Codec) BuildRouteStart(m RouteStartCmd) string {
	return c.Build("051", append(m.fields(), m.RouteID, m.StartTime, m.ExpectedCompletionTime, m.DutyID)...)
}
func DecodeRouteStart(f Frame) (RouteStartCmd, error) {
	r := newFieldReader(f)
	m := RouteStartCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), RouteID: r.next(), StartTime: r.next(), ExpectedCompletionTime: r.next(), DutyID: r.next()}
	return m, r.err
}

type RouteEndCmd struct {
	deviceCmdPrefix
	RouteID   string
	StartTime string
}

func (c *Codec) BuildRouteEndCmd(m RouteEndCmd) string {
	return c.Build("050", append(m.fields(), m.RouteID, m.StartTime)...)
}
func DecodeRouteEndCmd(f Frame) (RouteEndCmd, error) {
	r := newFieldReader(f)
	m := RouteEndCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), RouteID: r.next(), StartTime: r.next()}
	return m, r.err
}

type RequestHealthPacketCmd struct{ deviceCmdPrefix }

func (c *Codec) BuildRequestHealthPacket(p deviceCmdPrefix) string {
	return c.Build("065", p.fields()...)
}

type SnapshotFileUploadCmd struct {
	deviceCmdPrefix
	FromDateTime string // YYMMDDHHMM
	ToDateTime   string
	Channel      int
	Video        bool // false: snapshot, true: video
}

func (c *Codec) BuildSnapshotFileUpload(m SnapshotFileUploadCmd) string {
	return c.Build("070", append(m.fields(), m.FromDateTime, m.ToDateTime, itoa(m.Channel), boolFlag01(m.Video))...)
}
func DecodeSnapshotFileUpload(f Frame) (SnapshotFileUploadCmd, error) {
	r := newFieldReader(f)
	m := SnapshotFileUploadCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r), FromDateTime: r.next(), ToDateTime: r.next(), Channel: r.nextInt()}
	m.Video = r.next() == "1"
	return m, r.err
}

// --- Common ack, CCU->OBU (section 100) ---

type CommonAckCCU struct {
	deviceCmdPrefix
	Success     bool
	MessageType string
}

// NewCommonAckCCU builds a CommonAckCCU for use with BuildCommonAckCCU.
// dateTime should be in the doc's CCU->OBU format, YYMMDDHHMMSS (see
// FormatDateTime). messageType is the doc's per-command code identifying
// which OBU->CCU packet is being acknowledged (e.g. "4" for a location
// report, "HLT" for a health packet — see section 100's sample table).
func NewCommonAckCCU(vehicle, imei, dateTime string, success bool, messageType string) CommonAckCCU {
	return CommonAckCCU{deviceCmdPrefix{Vehicle: vehicle, IMEI: imei, DateTime: dateTime}, success, messageType}
}

func (c *Codec) BuildCommonAckCCU(m CommonAckCCU) string {
	return c.Build("ACK", append(m.fields(), boolFlag01(m.Success), m.MessageType)...)
}
func DecodeCommonAckCCU(f Frame) (CommonAckCCU, error) {
	r := newFieldReader(f)
	m := CommonAckCCU{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	m.Success = r.next() == "1"
	m.MessageType = r.next()
	return m, r.err
}

// --- Emergency alert response, CCU->OBU (section 44) ---

// EmergencyAckCmd is the server's response to an OBU's $EPB emergency alert
// (section 6), sent once help has been dispatched/acknowledged so the OBU
// can stop retransmitting the alert.
type EmergencyAckCmd struct {
	deviceCmdPrefix
	Success bool
}

// NewEmergencyAckCmd builds an EmergencyAckCmd for use with
// BuildEmergencyAck. dateTime should be in the doc's CCU->OBU format,
// YYMMDDHHMMSS (see FormatDateTime).
func NewEmergencyAckCmd(vehicle, imei, dateTime string, success bool) EmergencyAckCmd {
	return EmergencyAckCmd{deviceCmdPrefix{Vehicle: vehicle, IMEI: imei, DateTime: dateTime}, success}
}

func (c *Codec) BuildEmergencyAck(m EmergencyAckCmd) string {
	return c.Build("EMR", append(m.fields(), boolFlag01(m.Success))...)
}
func DecodeEmergencyAck(f Frame) (EmergencyAckCmd, error) {
	r := newFieldReader(f)
	m := EmergencyAckCmd{deviceCmdPrefix: decodeDeviceCmdPrefix(r)}
	m.Success = r.next() == "1"
	return m, r.err
}
