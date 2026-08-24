package n9m

import "encoding/json"

// Envelope is the JSON body carried by PayloadCommand frames. Exactly one of
// Parameter (request) or Response (reply) is normally populated.
type Envelope struct {
	Module    string          `json:"MODULE"`
	Operation string          `json:"OPERATION"`
	Session   string          `json:"SESSION,omitempty"`
	Parameter json.RawMessage `json:"PARAMETER,omitempty"`
	Response  json.RawMessage `json:"RESPONSE,omitempty"`
}

// IsResponse reports whether this envelope carries a RESPONSE object rather
// than a request PARAMETER.
func (e Envelope) IsResponse() bool { return len(e.Response) > 0 }

// key identifies a request/response pair by MODULE+OPERATION, which is how
// the N9M protocol correlates replies (there is no numeric request ID for
// most commands).
func (e Envelope) key() string { return e.Module + "/" + e.Operation }

// ErrorResponse is the common {ERRORCODE, ERRORCAUSE} shape embedded in most
// RESPONSE payloads.
type ErrorResponse struct {
	ErrorCode  int    `json:"ERRORCODE"`
	ErrorCause string `json:"ERRORCAUSE"`
}

// Well-known MODULE/OPERATION pairs from the Chemito N9M protocol doc.
const (
	ModuleCertificate = "CERTIFICATE"
	ModuleMediaStream = "MEDIASTREAMMODEL"
	ModuleAVSM        = "AVSM"
	ModuleDeviceEM    = "DEVEMM"
	ModuleEvent       = "EVEM"
	ModuleConfigModel = "CONFIGMODEL"

	OpConnect            = "CONNECT"
	OpKeepAlive          = "KEEPALIVE"
	OpCreateStream       = "CREATESTREAM"
	OpMediaTaskStart     = "MEDIATASKSTART"
	OpMediaTaskStop      = "MEDIATASKSTOP"
	OpRequestAliveVideo  = "REQUESTALIVEVIDEO"
	OpControlStream      = "CONTROLSTREAM"
	OpGetIFrame          = "GETIFRAME"
	OpRequestTalk        = "REQUESTTALK"
	OpControlTalk        = "CONTROLTALK"
	OpSendAlarmInfo      = "SENDALARMINFO"
	OpQueryGeneralStatus = "QUERYDEVGENERALSTATUS"
)

// ConnectParams is the request body for CERTIFICATE/CONNECT (device -> server
// authentication handshake).
type ConnectParams struct {
	NET      int    `json:"NET"` // 0 wired, 1 wifi, 2 3G/4G
	DevName  string `json:"DEVNAME"`
	CarNum   string `json:"CARNUM,omitempty"`
	CID      string `json:"CID,omitempty"`
	CPN      string `json:"CPN,omitempty"` // company name
	DSNO     string `json:"DSNO"`          // device serial number
	UNO      string `json:"UNO,omitempty"` // driver ID
	UName    string `json:"UNAME,omitempty"`
	Channel  int    `json:"CHANNEL,omitempty"`
	EV       string `json:"EV,omitempty"` // evidence-upload protocol version
	DevClass int    `json:"DEVCLASS,omitempty"`
}

// ConnectResponse is CERTIFICATE/CONNECT's RESPONSE payload.
type ConnectResponse struct {
	ErrorResponse
	DevType int    `json:"DEVTYPE"`
	PRO     string `json:"PRO"`
	VCode   string `json:"VCODE"`
	MaskCmd int    `json:"MASKCMD"`
}

// CreateStreamParams is the request body for CERTIFICATE/CREATESTREAM, sent
// on a freshly-opened media (2nd) TCP connection to bind it to a signaling
// session.
type CreateStreamParams struct {
	StreamName string `json:"STREAMNAME"`
	DSNO       string `json:"DSNO"`
	DevType    string `json:"DEVTYPE,omitempty"`
	Vision     string `json:"VISION,omitempty"`
}

// MediaTaskStartParams is CERTIFICATE/MEDIATASKSTART's payload: notifies the
// media-channel peer that streaming is about to begin for StreamName.
type MediaTaskStartParams struct {
	CSRC       string `json:"CSRC,omitempty"`
	IPAndPort  string `json:"IPANDPORT"`
	PT         int    `json:"PT"` // PayloadType of the media that will follow
	SSRC       int    `json:"SSRC"`
	StreamName string `json:"STREAMNAME"`
}

// MediaTaskStopParams is MEDIATASKSTOP's payload, same shape as start plus an
// error result.
type MediaTaskStopParams struct {
	MediaTaskStartParams
	ErrorCode  int    `json:"ERRORCODE"`
	ErrorCause string `json:"ERRORCAUSE"`
}

// RequestAliveVideoParams requests a live-preview media stream from a
// device.
type RequestAliveVideoParams struct {
	SSRC       int    `json:"SSRC,omitempty"`
	StreamName string `json:"STREAMNAME"`
	StreamType int    `json:"STREAMTYPE"` // 0 sub, 1 main, 2 mobile
	Channel    uint32 `json:"CHANNEL"`    // bitmask, bit0 = channel 1
	AudioValid uint32 `json:"AUDIOVALID,omitempty"`
	IPAndPort  string `json:"IPANDPORT"`
	Serial     uint32 `json:"SERIAL,omitempty"`
}

// RequestAliveVideoResponse is the device's reply to REQUESTALIVEVIDEO.
type RequestAliveVideoResponse struct {
	ErrorResponse
	SSRC       int    `json:"SSRC"`
	StreamName string `json:"STREAMNAME"`
	StreamType int    `json:"STREAMTYPE"`
}

// StreamControlCmd is the CMD field of CONTROLSTREAM. The doc defines all
// seven values below, but adds "We support 0, 3 and 6 only currently" —
// Resume/Pause/Audio/FrameRate (1/2/4/5) are part of the protocol but not
// implemented by real Chemito devices as of this doc's writing.
type StreamControlCmd int

const (
	StreamCmdStop      StreamControlCmd = 0
	StreamCmdResume    StreamControlCmd = 1
	StreamCmdPause     StreamControlCmd = 2
	StreamCmdSwitch    StreamControlCmd = 3 // switch STREAMTYPE
	StreamCmdAudio     StreamControlCmd = 4 // toggle audio per AUDIOVALID bitmask
	StreamCmdFrameRate StreamControlCmd = 5
	StreamCmdSendMode  StreamControlCmd = 6
)

// ControlStreamParams controls an in-progress live-video task.
type ControlStreamParams struct {
	PT         int              `json:"PT"`
	SSRC       int              `json:"SSRC"`
	StreamName string           `json:"STREAMNAME"`
	Cmd        StreamControlCmd `json:"CMD"`
	StreamType int              `json:"STREAMTYPE,omitempty"` // valid when Cmd == StreamCmdSwitch
	AudioValid uint32           `json:"AUDIOVALID,omitempty"` // valid when Cmd == StreamCmdAudio
	FrameMode  int              `json:"FRAMEMODE,omitempty"`
}

// GetIFrameParams requests an immediate I-frame on a live channel.
type GetIFrameParams struct {
	StreamType int    `json:"STREAMTYPE"`
	Channel    uint32 `json:"CHANNEL"`
}

// RequestTalkParams starts a two-way intercom session.
type RequestTalkParams struct {
	CSRC           string `json:"CSRC,omitempty"`
	SSRC           int    `json:"SSRC"`
	Serial         int    `json:"SERIAL,omitempty"`
	StreamName     string `json:"STREAMNAME"`
	Mode           int    `json:"MODE"` // 0 intercom
	Channel        uint32 `json:"CHANNEL,omitempty"`
	SoundMode      int    `json:"SOUNDMODE"` // 0 mono, 1 stereo
	ChannelTotal   int    `json:"CHANNELTOTAL"`
	SamplingRate   int    `json:"SAMPLINGRATE"`
	SamplingFigure int    `json:"SAMPLINGFIGURE"` // 0=8bit,1=16bit,2=32bit
	AudioFormat    int    `json:"AUDIOFORMAT"`    // see Audio_Encoding_Format table
	AudioSource    int    `json:"AUDIOSOURCE"`    // 1 camera pick-up, 2 microphone
	AudioFrameLen  int    `json:"AUDIOFRAMELEN"`
	IPAndPort      string `json:"IPANDPORT,omitempty"`
}

// ControlTalkParams stops an in-progress intercom session (CMD 0 is the only
// documented value).
type ControlTalkParams struct {
	CSRC       string `json:"CSRC,omitempty"`
	SSRC       int    `json:"SSRC"`
	StreamName string `json:"STREAMNAME"`
	Cmd        int    `json:"CMD"`
}

// AlarmCommonParams is the subset of EVEM/SENDALARMINFO's PARAMETER fields
// (see "General Alarm Report Format") needed to build the acknowledgement;
// alarm-type-specific fields are ignored here.
type AlarmCommonParams struct {
	AlarmType int    `json:"ALARMTYPE"`
	CmdType   int    `json:"CMDTYPE"`
	AlarmUID  int    `json:"ALARMUID"`
	CmdNo     uint32 `json:"CMDNO"`
	Run       int    `json:"RUN"`
}

// AlarmAckResponse is the RESPONSE payload the server must echo back for
// EVEM/SENDALARMINFO.
type AlarmAckResponse struct {
	AlarmType int    `json:"ALARMTYPE"`
	CmdType   int    `json:"CMDTYPE"`
	AlarmUID  int    `json:"ALARMUID"`
	CmdNo     uint32 `json:"CMDNO"`
	Run       int    `json:"RUN"`
	ErrorCode int    `json:"ERRORCODE"`
}

// AckFrom builds the acknowledgement response for an alarm report.
func (p AlarmCommonParams) AckFrom() AlarmAckResponse {
	return AlarmAckResponse{
		AlarmType: p.AlarmType,
		CmdType:   p.CmdType,
		AlarmUID:  p.AlarmUID,
		CmdNo:     p.CmdNo,
		Run:       p.Run,
		ErrorCode: 0,
	}
}
