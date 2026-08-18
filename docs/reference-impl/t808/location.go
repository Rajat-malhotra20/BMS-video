package t808

const MsgIDLocationReport = 0x0200

// Alarm flag bits (Table 24), a DWORD bitmask.
const (
	AlarmEmergency           = 1 << 0 // cleared after response
	AlarmOverSpeed           = 1 << 1
	AlarmFatigueDriving      = 1 << 2
	AlarmDangerWarning       = 1 << 3 // cleared after response
	AlarmGNSSModuleFailure   = 1 << 4
	AlarmGNSSAntennaCut      = 1 << 5
	AlarmGNSSAntennaShort    = 1 << 6
	AlarmMainPowerUndervolt  = 1 << 7
	AlarmMainPowerOff        = 1 << 8
	AlarmBDCFailure          = 1 << 9
	AlarmTTSFailure          = 1 << 10
	AlarmCameraFailure       = 1 << 11
	AlarmSpeedWarning        = 1 << 13
	AlarmFatigueWarning      = 1 << 14
	AlarmDailyDrivingTimeout = 1 << 18
	AlarmOvertimeParking     = 1 << 19
	AlarmInOutArea           = 1 << 20 // cleared after response
	AlarmInOutRoute          = 1 << 21 // cleared after response
	AlarmRouteTimeAbnormal   = 1 << 22 // cleared after response
	AlarmRouteDeviation      = 1 << 23
	AlarmVSSFailure          = 1 << 24
	AlarmOilAbnormal         = 1 << 25
	AlarmVehicleStolen       = 1 << 26
	AlarmIllegalIgnition     = 1 << 27 // cleared after response
	AlarmIllegalDisplacement = 1 << 28 // cleared after response
	AlarmCollisionWarning    = 1 << 29
	AlarmRolloverWarning     = 1 << 30
	AlarmIllegalDoorOpening  = 1 << 31 // cleared after response
)

// Status flag bits (Table 25), a DWORD bitmask.
const (
	StatusACCOn                  = 1 << 0
	StatusPositioned             = 1 << 1
	StatusSouthLatitude          = 1 << 2
	StatusWestLongitude          = 1 << 3
	StatusOutage                 = 1 << 4
	StatusLatLongEncrypted       = 1 << 5
	StatusOilCircuitDisconnected = 1 << 11
	StatusVehicleCircuitAbnormal = 1 << 12
	StatusOBUDoorLocked          = 1 << 13
	StatusDoor1On                = 1 << 14 // Front Door
	StatusDoor2On                = 1 << 15 // Middle Door
	StatusDoor3On                = 1 << 16 // Back Door
	StatusDoor4On                = 1 << 17 // Driver Door
	StatusDoor5On                = 1 << 18 // Custom
	StatusGPSPositioning         = 1 << 19
	StatusGLONASSPositioning     = 1 << 21
)

// LoadStatus extracts the 2-bit load-status field (bits 10-11 per Table 25's
// grouping); see the doc's own row-grouping ambiguity noted in the package
// extraction notes.
func LoadStatus(status uint32) byte { return byte((status >> 10) & 0x3) }

const (
	LoadStatusEmpty      = 0
	LoadStatusHalfLoaded = 1
	LoadStatusFullLoaded = 3
)

// Known additional-information (location extension) IDs, Table 27.
const (
	ExtMileage          = 0x01 // 4 bytes, DWORD, 1/10 km
	ExtFuel             = 0x02 // 2 bytes, WORD, 1/10 L
	ExtRecorderSpeed    = 0x03 // driving-record function speed
	ExtManualAlarmEvent = 0x04 // WORD, manual-confirm alarm event ID
	ExtOverSpeedAlarm   = 0x11 // Table 28
	ExtInOutAreaAlarm   = 0x12 // Table 29
	ExtRouteTimeAlarm   = 0x13 // Table 30
	ExtVehicleSignals   = 0x25 // Table 31 bit flags
	ExtIOStatus         = 0x2A // Table 32 bit flags
	ExtAnalog           = 0x2B // bit0-15=AD0, bit16-31=AD1
	ExtSignalStrength   = 0x30 // 1 byte
	ExtSatelliteCount   = 0x31 // 1 byte
	ExtCustomLength     = 0xE0
	ExtAlertPIS         = 0xEA // 2 bytes: alert status byte + PIS status byte
)

// LocationExtension is one generic TLV additional-information block
// (Table 26).
type LocationExtension struct {
	ID   byte
	Data []byte
}

// LocationReport is message 0x0200 (OBU -> Server).
type LocationReport struct {
	AlarmFlags uint32
	Status     uint32
	// Latitude/Longitude are stored pre-multiplied by 1e6 as the wire format
	// dictates (e.g. 19973921 for 19.973921 degrees); use LatitudeDeg/
	// LongitudeDeg for the human-readable float.
	Latitude   int32
	Longitude  int32
	Elevation  uint16 // meters
	Speed      uint16 // 1/10 km/h
	Direction  uint16 // 0-359 degrees, clockwise from north
	Time       BCDDateTime
	Extensions []LocationExtension
}

func (l LocationReport) LatitudeDeg() float64  { return float64(l.Latitude) / 1e6 }
func (l LocationReport) LongitudeDeg() float64 { return float64(l.Longitude) / 1e6 }

func BuildLocationReport(m LocationReport) ([]byte, error) {
	var w byteWriter
	w.u32(m.AlarmFlags)
	w.u32(m.Status)
	w.u32(uint32(m.Latitude))
	w.u32(uint32(m.Longitude))
	w.u16(m.Elevation)
	w.u16(m.Speed)
	w.u16(m.Direction)
	w.dateTime(m.Time)
	for _, ext := range m.Extensions {
		w.u8(ext.ID)
		w.u8(byte(len(ext.Data)))
		w.raw(ext.Data)
	}
	return w.bytesOut()
}

func DecodeLocationReport(body []byte) (LocationReport, error) {
	r := newByteReader(body)
	m := LocationReport{
		AlarmFlags: r.u32(),
		Status:     r.u32(),
		Latitude:   int32(r.u32()),
		Longitude:  int32(r.u32()),
		Elevation:  r.u16(),
		Speed:      r.u16(),
		Direction:  r.u16(),
	}
	var t [6]byte
	copy(t[:], r.bytesN(6))
	m.Time = decodeBCDDateTime(t)
	for !r.atEnd() && r.err == nil {
		id := r.u8()
		n := int(r.u8())
		data := r.bytesN(n)
		if r.err != nil {
			break
		}
		m.Extensions = append(m.Extensions, LocationExtension{ID: id, Data: data})
	}
	return m, r.err
}

// Find returns the first extension with the given ID, if present.
func (l LocationReport) Find(id byte) (LocationExtension, bool) {
	for _, e := range l.Extensions {
		if e.ID == id {
			return e, true
		}
	}
	return LocationExtension{}, false
}

// AlertPISStatus decodes the 0xEA extension (Tamper/BDC alert + PIS door
// status), added to the protocol after the base spec per the source doc.
type AlertPISStatus struct {
	TamperAlert bool
	BDCAlert    bool
	PIS1FDU     bool
	PIS2RDU     bool
	PIS3IDU     bool
	PIS4SDU     bool
}

func DecodeAlertPISStatus(data []byte) AlertPISStatus {
	var s AlertPISStatus
	if len(data) < 1 {
		return s
	}
	alert := data[0]
	s.TamperAlert = alert&0x01 != 0
	s.BDCAlert = alert&0x02 != 0
	if len(data) < 2 {
		return s
	}
	pis := data[1]
	s.PIS1FDU = pis&0x01 != 0
	s.PIS2RDU = pis&0x02 != 0
	s.PIS3IDU = pis&0x04 != 0
	s.PIS4SDU = pis&0x08 != 0
	return s
}

func EncodeAlertPISStatus(s AlertPISStatus) []byte {
	var alert, pis byte
	if s.TamperAlert {
		alert |= 0x01
	}
	if s.BDCAlert {
		alert |= 0x02
	}
	if s.PIS1FDU {
		pis |= 0x01
	}
	if s.PIS2RDU {
		pis |= 0x02
	}
	if s.PIS3IDU {
		pis |= 0x04
	}
	if s.PIS4SDU {
		pis |= 0x08
	}
	return []byte{alert, pis}
}
