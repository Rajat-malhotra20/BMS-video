package n9m

import (
	"encoding/json"
	"testing"
)

func TestPeekAlarmTypeAndDecodeSpeeding(t *testing.T) {
	raw := json.RawMessage(`{
		"ALARMAS": 1, "ALARMCOUNT": 1, "ALARMNAME": "Over Speed", "ALARMTYPE": 8,
		"ALARMUID": 11, "AT": 0, "ATYPE": 1, "CMDNO": 1558970383, "CMDTYPE": 1,
		"CONTINUETIME": 0, "CSP": 7700, "CURRENTTIME": 1656522240,
		"EVTUUID": "7705e06e-5a50-431a-b825-7292ab51eb4a", "I": 0, "L": 0,
		"LD": 5, "MAXS": 77, "MAXSP": 5000, "MINS": 0, "MINSP": 0,
		"P": {"C": 0, "J": "0.000000", "S": 7700, "T": "20000000000000", "V": 2, "W": "0.000000"},
		"REAL": 0, "RUN": 23788, "SECNO": 0, "SER": "SPD", "TRIGGERTYPE": 1
	}`)

	got, err := PeekAlarmType(raw)
	if err != nil {
		t.Fatalf("PeekAlarmType: %v", err)
	}
	if got != AlarmTypeSpeeding {
		t.Fatalf("expected AlarmTypeSpeeding, got %d", got)
	}

	var a AlarmSpeeding
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal AlarmSpeeding: %v", err)
	}
	if a.CSP != 7700 || a.LD != 5 || a.Ser != "SPD" || a.AlarmName != "Over Speed" {
		t.Fatalf("unexpected decode: %+v", a)
	}
	if a.P.S != 7700 {
		t.Fatalf("expected embedded location speed 7700, got %d", a.P.S)
	}
}

func TestAlarmIOShadowsBaseLanguageField(t *testing.T) {
	raw := json.RawMessage(`{
		"ALARMTYPE": 4, "SER": "S1", "SNO": 1, "USE": 23, "L": "line-42",
		"PC": 5, "DN": ["D1"], "MN": ["M1"],
		"P": {"C":0,"J":"0","S":0,"T":"x","V":0,"W":"0"}
	}`)

	var a AlarmIO
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal AlarmIO: %v", err)
	}
	if a.L != "line-42" {
		t.Fatalf("expected shadowed L to decode as string 'line-42', got %q", a.L)
	}
	if a.PC != 5 || len(a.DN) != 1 || a.DN[0] != "D1" {
		t.Fatalf("unexpected decode: %+v", a)
	}
}

func TestAlarmVideoLossLCHArray(t *testing.T) {
	raw := json.RawMessage(`{"ALARMTYPE":0,"CHANNEL":1,"CHANNELMASK":1,"LCH":[31],"SER":"VL"}`)
	var a AlarmVideoLoss
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal AlarmVideoLoss: %v", err)
	}
	if len(a.LCH) != 1 || a.LCH[0] != 31 {
		t.Fatalf("unexpected LCH: %+v", a.LCH)
	}
}

func TestAlarmEmergencyLCHIsScalar(t *testing.T) {
	raw := json.RawMessage(`{"ALARMTYPE":7,"LCH":0,"SER":"Urgent"}`)
	var a AlarmEmergency
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal AlarmEmergency: %v", err)
	}
	if a.LCH != 0 {
		t.Fatalf("unexpected LCH: %d", a.LCH)
	}
}
