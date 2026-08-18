package n9m

import (
	"encoding/json"
	"fmt"
)

// This file covers the individual EVEM/SENDALARMINFO alarm-type payloads
// from the "Alarm Report" chapter. Every alarm shares a common field set
// (AlarmBase) plus type-specific fields; PeekAlarmType lets a caller decode
// just enough to pick the right concrete struct before fully unmarshaling.

// AlarmType enumerates EVEM/SENDALARMINFO's ALARMTYPE values that this
// package has typed structs for. Many other values exist in the wider N9M
// alarm space; unrecognized ones can still be handled via AlarmBase alone.
type AlarmType int

const (
	AlarmTypeVideoLoss       AlarmType = 0
	AlarmTypeCoveredVideo    AlarmType = 1
	AlarmTypeMotionDetection AlarmType = 2
	AlarmTypeMemoryException AlarmType = 3
	AlarmTypeIO              AlarmType = 4
	AlarmTypeEmergency       AlarmType = 7
	AlarmTypeSpeeding        AlarmType = 8
	AlarmTypeVoltage         AlarmType = 9
	AlarmTypeGeoFence        AlarmType = 17
	AlarmTypeACC             AlarmType = 18
	AlarmTypeDSM             AlarmType = 56
	AlarmTypeTemperature     AlarmType = 61
	AlarmTypeHumidity        AlarmType = 116
)

// AlarmLocation is the "P" GPS sub-object embedded in every alarm report.
type AlarmLocation struct {
	C int    `json:"C"` // direction, degrees * 100
	J string `json:"J"` // longitude, decimal string
	S int    `json:"S"` // speed, km/h * 100
	T string `json:"T"` // local time, YYYYMMDDHHMMSS
	V int    `json:"V"` // 0 valid, 1 invalid, 2 no GPS module
	W string `json:"W"` // latitude, decimal string
}

// AlarmBase is the field set common to every EVEM/SENDALARMINFO report.
type AlarmBase struct {
	AlarmAs     int           `json:"ALARMAS"` // 0 important,1 general,2 emergency
	AlarmCount  int           `json:"ALARMCOUNT"`
	AlarmType   AlarmType     `json:"ALARMTYPE"`
	AlarmUID    int           `json:"ALARMUID"`
	CmdNo       uint32        `json:"CMDNO"`
	CmdType     int           `json:"CMDTYPE"` // 0 remove,1 start,2 warning,3 duration
	CurrentTime int64         `json:"CURRENTTIME"`
	EvtUUID     string        `json:"EVTUUID"`
	L           int           `json:"L"` // language code
	P           AlarmLocation `json:"P"`
	Real        int           `json:"REAL"`
	Run         int           `json:"RUN"`
	SecNo       int           `json:"SECNO"`
	Ser         string        `json:"SER"`
	TriggerType int           `json:"TRIGGERTYPE"` // 0 manual,1 automatic
}

// AlarmTypePeek is the minimal shape needed to route a raw alarm PARAMETER
// to the right concrete struct.
type AlarmTypePeek struct {
	AlarmType AlarmType `json:"ALARMTYPE"`
}

// PeekAlarmType decodes just enough of a raw EVEM/SENDALARMINFO PARAMETER to
// determine which concrete alarm struct to unmarshal it into.
func PeekAlarmType(raw json.RawMessage) (AlarmType, error) {
	var p AlarmTypePeek
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("n9m: peek alarm type: %w", err)
	}
	return p.AlarmType, nil
}

// AlarmVideoLoss is ALARMTYPE 0.
type AlarmVideoLoss struct {
	AlarmBase
	AlarmName   string `json:"ALARMNAME"`
	Channel     int    `json:"CHANNEL"`
	ChannelMask int    `json:"CHANNELMASK"`
	LCH         []int  `json:"LCH"`
}

// AlarmCoveredVideo is ALARMTYPE 1.
type AlarmCoveredVideo struct {
	AlarmBase
	AlarmName   string `json:"ALARMNAME"`
	Channel     int    `json:"CHANNEL"`
	ChannelMask int    `json:"CHANNELMASK"`
	LCH         []int  `json:"LCH"`
}

// AlarmMotionDetection is ALARMTYPE 2.
type AlarmMotionDetection struct {
	AlarmBase
	AlarmName   string `json:"ALARMNAME"`
	Channel     int    `json:"CHANNEL"`
	ChannelMask int    `json:"CHANNELMASK"`
	LCH         []int  `json:"LCH"`
}

// AlarmMemoryException is ALARMTYPE 3.
type AlarmMemoryException struct {
	AlarmBase
	StorageIndex int `json:"STORAGEINDEX"`
	StorageType  int `json:"STORAGETYPE"` // 0 HDD,1 USB,2 SD
	ErrorCode    int `json:"ERRORCODE"`   // 0 full,1 unformatted,2 R/W error,3 no record,4 no storage,5 unmounted,6 partition error,7 over capacity
}

// AlarmIO is ALARMTYPE 4. Note: L here (line number string, valid when
// USE==23) intentionally shadows AlarmBase.L (language code, an int) — Go's
// encoding/json resolves the ambiguity by preferring the shallower field, so
// this L is the one that (un)marshals.
type AlarmIO struct {
	AlarmBase
	SNO       int      `json:"SNO"`
	AlarmName string   `json:"ALARMNAME"`
	LCH       []int    `json:"LCH"`
	Use       int      `json:"USE"` // IO purpose code, see doc table
	L         string   `json:"L"`   // line number, valid when Use == 23
	PC        int      `json:"PC"`  // passenger count, valid when Use == 23
	DN        []string `json:"DN"`  // driver card numbers, valid when Use == 23
	MN        []string `json:"MN"`  // admin card numbers, valid when Use == 23
}

// AlarmEmergency is ALARMTYPE 7 (panic button).
type AlarmEmergency struct {
	AlarmBase
	AlarmName string `json:"ALARMNAME"`
	LCH       int    `json:"LCH"` // linked-channel bitmask
}

// AlarmSpeeding is ALARMTYPE 8.
type AlarmSpeeding struct {
	AlarmBase
	AlarmName    string `json:"ALARMNAME"`
	AType        int    `json:"ATYPE"` // 0 low-speed,1 high-speed,2 warning,3 instant high,4 rapid decel,5 recovery,6 parking,7 threshold,8 start
	CSP          int    `json:"CSP"`   // current speed *100
	MinSP        int    `json:"MINSP"`
	MaxSP        int    `json:"MAXSP"`
	MinS         int    `json:"MINS"`
	MaxS         int    `json:"MAXS"`
	AT           int    `json:"AT"` // location type
	I            int    `json:"I"`  // area/segment ID
	LD           int    `json:"LD"` // alarm level 0-5
	ContinueTime int    `json:"CONTINUETIME"`
}

// AlarmVoltage is ALARMTYPE 9.
type AlarmVoltage struct {
	AlarmBase
	V int `json:"V"` // current voltage *100
	S int `json:"S"` // 0 low voltage,1 high voltage
}

// AlarmGeoFence is ALARMTYPE 17 (in/out area or route).
type AlarmGeoFence struct {
	AlarmBase
	E   int    `json:"E"`  // event type bitmask: bit0 enter,1 exit,2 enter-line,3 exit-line,4 lane-departure,5 too-long/insufficient
	AC  []int  `json:"AC"` // per-event action bitmask
	AT  int    `json:"AT"` // 1 circular,2 rectangular,3 polygon,4 line
	ID  int    `json:"ID"`
	AN  string `json:"AN"`  // geo-fence name
	GEO string `json:"GEO"` // "0" off, "1" on; empty = fence doesn't control video
	TO  int    `json:"TO"`  // source number, starting from 1
}

// AlarmACC is ALARMTYPE 18 (harsh driving via accelerometer).
type AlarmACC struct {
	AlarmBase
	AlarmName string `json:"ALARMNAME"`
	LCH       int    `json:"LCH"` // linked-channel bitmask
	D         int    `json:"D"`   // direction: 0 X,1 Y,2 Z,3 collision,4 rollover,5 bump,6 rapid accel,7 rapid decel,8 G4,9 G5,10 sharp left,11 sharp right
	X         int    `json:"X"`   // *1000
	Y         int    `json:"Y"`   // *1000
	Z         int    `json:"Z"`   // *1000
	V         int    `json:"V"`   // valid for G4/G5
}

// AlarmDSM is ALARMTYPE 56 (driver-state-monitoring AI alarm).
type AlarmDSM struct {
	AlarmBase
	ST      int    `json:"ST"`      // alarm subtype, see doc's "DSM Alarm Sub-types" table
	SP      int    `json:"SP"`      // sensor location
	LCH     int    `json:"LCH"`     // linked-channel bitmask
	Lev     int    `json:"LEV"`     // 1 level-1, 2 level-2
	LDWType int    `json:"LDWTYPE"` // valid when ST == 5 (lane departure): 0 left, 1 right
	KW      string `json:"KW"`      // valid when ST == 30 (uncivilized words)
	LSP     string `json:"LSP"`     // valid when ST == 7 (speeding warning): recognized speed limit
}

// AlarmTemperature is ALARMTYPE 61.
type AlarmTemperature struct {
	AlarmBase
	AlarmName string `json:"ALARMNAME"`
	LCH       int    `json:"LCH"`
	SNO       int    `json:"SNO"` // sensor number, 0-3
	CT        int    `json:"CT"`  // max threshold *100
	SCT       int    `json:"SCT"` // min threshold *100
	PCT       int    `json:"PCT"` // offset *100
	CWD       int    `json:"CWD"` // warming-rise threshold *100
	CWT       int    `json:"CWT"` // warming duration, seconds
}

// AlarmHumidity is ALARMTYPE 116.
type AlarmHumidity struct {
	AlarmBase
	AlarmName string `json:"ALARMNAME"`
	LCH       int    `json:"LCH"`
	SNO       int    `json:"SNO"`
	LHDY      int    `json:"LHDY"` // max threshold *100
	SHDY      int    `json:"SHDY"` // min threshold *100
	PHDY      int    `json:"PHDY"` // offset *100
	CHDY      int    `json:"CHDY"` // current humidity *100
}
