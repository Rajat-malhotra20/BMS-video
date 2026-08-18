package n9m

// This file covers DEVEMM/QUERYDEVGENERALSTATUS, DEVEMM/GETDEVINFOSTATUS,
// DEVEMM/GETUPDATEIOSTATUS + DEVEMM/UPDATEIOSTATUS, and
// DEVEMM/SETCONTROLDEVCMD from the "Device Status" chapter.

// Additional MODULE/OPERATION pairs (device status + control).
const (
	OpGetDevInfoStatus  = "GETDEVINFOSTATUS"
	OpGetUpdateIOStatus = "GETUPDATEIOSTATUS"
	OpUpdateIOStatus    = "UPDATEIOSTATUS" // device-initiated push, no request
	OpSetControlDevCmd  = "SETCONTROLDEVCMD"
)

// --- QUERYDEVGENERALSTATUS ---

// GeneralStatusQuery is QUERYDEVGENERALSTATUS's request PARAMETER.
type GeneralStatusQuery struct {
	QMask  int `json:"QMASK"` // bitmask selecting which sub-objects to return
	Serial int `json:"SERIAL"`
}

// CommModuleStatus (WLM) is one 2G/3G/4G communication module's status.
type CommModuleStatus struct {
	EN    int    `json:"EN"`   // 0 on, 1 off
	MS    int    `json:"MS"`   // 0 module exists, 1 doesn't
	SIMS  int    `json:"SIMS"` // 0 none,1 exists,2 invalid,3 valid
	BHS   int    `json:"BHS"`  // dialing status, 0-6
	IMEI  string `json:"IMEI"`
	IMSI  string `json:"IMSI"`
	NM    int    `json:"NM"` // network standard
	NT    int    `json:"NT"` // 0 none,1 mixed,2 2G,3 3G,4 4G
	NN    string `json:"NN"` // network type name
	IP    string `json:"IP"`
	SVER  string `json:"SVER"`
	RSSI  int    `json:"RSSI"`
	MN    int    `json:"MN"` // module model
	ICCID string `json:"ICCID"`
}

// WiFiModuleStatus (WFM) is one WiFi module's status.
type WiFiModuleStatus struct {
	EN int    `json:"EN"` // 0 off,1 station on,2 AP on (internal module only)
	MD int    `json:"MD"` // 0 station,1 AP
	MS int    `json:"MS"`
	ID string `json:"ID"` // ESSID
	SL int    `json:"SL"` // signal level 1-5 (MD=0), invalid when MD=1
	NN string `json:"NN"`
}

// PositioningStatus (GPSM) is the GNSS positioning module status.
type PositioningStatus struct {
	GS  int `json:"GS"`  // 0 valid,1 invalid,2 no module
	GT  int `json:"GT"`  // 0 GPS,1 Beidou,2 GLONASS,3 Galileo
	GPN int `json:"GPN"` // GPS satellite count
	BDN int `json:"BDN"`
	GLN int `json:"GLN"`
	GAN int `json:"GAN"`
	CTS int `json:"CTS"` // antenna status: 0 normal,1 open,2 short
}

// BluetoothDevice is one entry in BluetoothStatus.ConnList.
type BluetoothDevice struct {
	Stat    int    `json:"STAT"` // 0 not connected,1 matched,2 connected
	Name    string `json:"NAME"`
	Address string `json:"ADDRESS"`
}

// BluetoothStatus (BTM) is the Bluetooth module status.
type BluetoothStatus struct {
	ConnList []BluetoothDevice `json:"CONNLIST"`
}

// ChannelStatus (CHS) is one camera/recording channel's status.
type ChannelStatus struct {
	Cams int `json:"CAMS"` // 0 normal,1 not connected,2 not enabled
	Recs int `json:"RECS"` // 0-3, see doc
}

// StorageStatus (STOR) is one storage device's status.
type StorageStatus struct {
	SV   int `json:"SV"`   // 0 invalid,1 valid
	ST   int `json:"ST"`   // 0 HDD,1 SD,2 USB,3 eSATA,4 array
	SS   int `json:"SS"`   // 0 no storage,1 unformatted,2 full,3 not recording,4 recording
	SIDX int `json:"SIDX"` // serial number
	SP   int `json:"SP"`   // 0 internal,1 external
}

// CarKeyStatus (VKS) is the HDD-lock/ignition status.
type CarKeyStatus struct {
	CKS int `json:"CKS"` // 0 off,1 on (ignition)
	HDK int `json:"HDK"` // 0 unlocked,1 locked
}

// IOStatusInfo (IOS) is one digital I/O line's status.
type IOStatusInfo struct {
	Name   string `json:"NAME"`
	NSer   string `json:"NSER"`
	SNO    int    `json:"SNO"`
	Status int    `json:"STATUS"` // 0 low,1 high,2 open,3 short
	Usg    int    `json:"USG"`    // IO purpose code, see doc table
}

// CentralServerStatus (CMSC) is one configured server connection's status.
type CentralServerStatus struct {
	IDX    int    `json:"IDX"`
	EN     int    `json:"EN"`  // 0 disabled,1 enabled
	PTO    int    `json:"PTO"` // 0 N9M, 2 808, 3 O&M
	Status int    `json:"STATUS"`
	Addr   string `json:"ADDR"`
	NT     int    `json:"NT"` // 0 wired,1 wifi,2 comm1,3 comm2,4 self-adaptive
}

// CANBoxStatus (CANBOX) is the CAN-BOX module status.
type CANBoxStatus struct {
	V  string `json:"V"`
	CS int    `json:"CS"` // 0 not connected,1 connected
}

// GeneralStatusResponse is QUERYDEVGENERALSTATUS's RESPONSE payload.
type GeneralStatusResponse struct {
	WLM     []CommModuleStatus    `json:"WLM,omitempty"`
	WFM     []WiFiModuleStatus    `json:"WFM,omitempty"`
	GPSM    *PositioningStatus    `json:"GPSM,omitempty"`
	BTM     *BluetoothStatus      `json:"BTM,omitempty"`
	CHS     []ChannelStatus       `json:"CHS,omitempty"`
	STOR    []StorageStatus       `json:"STOR,omitempty"`
	VKS     *CarKeyStatus         `json:"VKS,omitempty"`
	IOS     []IOStatusInfo        `json:"IOS,omitempty"`
	CMSC    []CentralServerStatus `json:"CMSC,omitempty"`
	CANBox  *CANBoxStatus         `json:"CANBOX,omitempty"`
	NetType int                   `json:"NETTYPE"`
	Serial  int                   `json:"SERIAL"`
}

// --- GETDEVINFOSTATUS ---

// StorageInfo (SINFO) is one storage device's usage summary.
type StorageInfo struct {
	DS int   `json:"DS"` // detailed status, see doc
	LS int64 `json:"LS"` // available size
	O  int   `json:"O"`  // 0 internal,1 external
	S  int   `json:"S"`  // 0 normal,1 fault
	T  int   `json:"T"`  // 0 HDD,1 USB,2 SD
	TS int64 `json:"TS"` // total size
}

// TrafficInfo (TRAFFIC) is one NIC's monthly traffic usage.
type TrafficInfo struct {
	T  int    `json:"T"`  // 0 wired,1 wifi,2 3G,3 4G-LTE
	I  string `json:"I"`  // NIC IMSI
	TS int64  `json:"TS"` // total KB
	TX int64  `json:"TX"` // sent KB
	RX int64  `json:"RX"` // received KB
}

// DevInfoStatus is GETDEVINFOSTATUS's "S" RESPONSE payload.
type DevInfoStatus struct {
	Alarm   int           `json:"ALARM"`
	BV      int           `json:"BV"` // battery voltage *100
	G3      int           `json:"G3"`
	G3S     int           `json:"G3S"`
	G4      int           `json:"G4"`
	G4S     int           `json:"G4S"`
	W       int           `json:"W"` // wifi status
	WS      int           `json:"WS"`
	V       int           `json:"V"`   // device voltage *100
	TD      int           `json:"TD"`  // device temperature *100
	TC      int           `json:"TC"`  // in-vehicle temperature *100
	S       int           `json:"S"`   // speed *100
	SU      int           `json:"SU"`  // 0 km/h,1 mi/h
	SW      int           `json:"SW"`  // 0 off,1 on (ignition)
	RE      []int         `json:"RE"`  // per-channel recording status
	T       string        `json:"T"`   // local time YYYYMMDDHHMMSS
	STC     int           `json:"STC"` // number of storage devices
	SINFO   []StorageInfo `json:"SINFO"`
	Traffic []TrafficInfo `json:"TRAFFIC"`
	VS      []int         `json:"VS"`  // per-channel video-loss status
	H       int           `json:"H"`   // humidity *10000
	WD      int           `json:"WD"`  // wind direction
	TM      string        `json:"TM"`  // total mileage (km)
	HTR     int           `json:"HTR"` // 0 normal,1 heating
}

// GetDevInfoStatusResponse wraps the S object and echoed SERIAL, matching
// the doc's RESPONSE shape ({"S": {...}, "SERIAL": n}).
type GetDevInfoStatusResponse struct {
	S      DevInfoStatus `json:"S"`
	Serial int           `json:"SERIAL"`
}

// --- IO status (GETUPDATEIOSTATUS / UPDATEIOSTATUS) ---

// IOLine is one sensor input's current status, as used by
// GetUpdateIOStatusResponse and UpdateIOStatusParams.
type IOLine struct {
	Name string `json:"NAME"`
	User string `json:"USER"` // abbreviation, e.g. "IO1"
	S    int    `json:"S"`    // 0 low,1 high,2 open,3 short
	U    int    `json:"U"`    // IO purpose code
}

// ACCStatus is the ignition status sub-object.
type ACCStatus struct {
	A int `json:"A"` // 0 flameout,1 ignition
}

// PulseInfo is the pulse counter sub-object.
type PulseInfo struct {
	N int `json:"N"`
}

// IOStatusPayload is the shared shape of GETUPDATEIOSTATUS's RESPONSE and
// UPDATEIOSTATUS's PARAMETER (device-initiated push).
type IOStatusPayload struct {
	Serial int       `json:"SERIAL,omitempty"`
	IO     []IOLine  `json:"IO"`
	ACC    ACCStatus `json:"ACC"`
	FN     PulseInfo `json:"FN"`
	FB     int       `json:"FB"` // panic button: 0 not occurred,1 occurred
}

// --- SETCONTROLDEVCMD ---

// DevControlCmd selects the SETCONTROLDEVCMD action.
type DevControlCmd int

const (
	DevControlRestart       DevControlCmd = 0
	DevControlSleepPowerOff DevControlCmd = 1
	DevControlPowerOff      DevControlCmd = 2
)

// SetControlDevCmdParams is SETCONTROLDEVCMD's request PARAMETER.
type SetControlDevCmdParams struct {
	CmdType  DevControlCmd `json:"CMDTYPE"`
	Serial   int           `json:"SERIAL"`
	Continue int           `json:"CONTINUE,omitempty"` // shutdown delay (s); valid when CmdType == DevControlSleepPowerOff
}

// SetControlDevCmdResponse is SETCONTROLDEVCMD's RESPONSE payload.
type SetControlDevCmdResponse struct {
	ErrorResponse
	CmdType DevControlCmd `json:"CMDTYPE"`
	Serial  int           `json:"SERIAL"`
}
