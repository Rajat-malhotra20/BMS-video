package sumith

import "testing"

func TestLoginRoundTrip(t *testing.T) {
	c := NewCodec()
	want := Login{
		Vehicle: "AP36TS1234", IMEI: "864819050843380", FirmwareVer: "11.0.20", ProtocolVer: "1.0.2",
		LastLocation: LastKnownLocation{GPSFix: 1, Date: "220714", Time: "050656", Latitude: 28.758963, LatDir: "N", Longitude: 77.6277844, LngDir: "E", SpeedKmh: 25},
	}
	raw := c.BuildLogin(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	if f.Token != "LGN" {
		t.Fatalf("expected token LGN, got %q", f.Token)
	}
	got, err := DecodeLogin(f)
	if err != nil {
		t.Fatalf("DecodeLogin: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestLocationRoundTrip(t *testing.T) {
	c := NewCodec()
	want := Location{
		FirmwareVersion: "11.0.20", PacketType: "NR", AlertID: 1, PacketStatus: "L",
		IMEI: "864819050843380", Vehicle: "AP36TS1234", GPSFix: 1, Date: "12032024", Time: "050402",
		Latitude: 17.47834833, LatDir: "N", Longitude: 78.57026333, LngDir: "E",
		SpeedKmh: 0.0, HeadingDeg: 191.0, Satellites: 18, AltitudeM: 562.245, PDOP: 1.29, HDOP: 0.65,
		NetworkOperator: "jionet", Ignition: 1, MainPowerStatus: 1, MainVoltage: 22.77, InternalBattery: 7.61,
		EmergencyStatus: 0, TamperAlert: "C", GSMSignal: 18, MCC: "404", MNC: "73", LAC: "A51", CellID: "AABF",
		NMR: "0#0#0#0#0#0#0#0#0#0#0#0", DigitalInputs: "0000", DigitalOutputs: "00", FrameNo: "000794",
	}
	raw := c.BuildLocation(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	got, err := DecodeLocation(f)
	if err != nil {
		t.Fatalf("DecodeLocation: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestHealthStatusRoundTrip(t *testing.T) {
	c := NewCodec()
	want := HealthStatus{
		OBUID: "000009", MDVRName: "Dik3339", VendorID: "3339", DateTime: "12032024093947",
		PrimaryIP: "49.207.2.123", SecondaryIP: "122.168.23.1", FirmwareVersion: "11.0.20", ProtocolVersion: "1.0.2",
		IMEI:           "864819050843380",
		Storage1Status: 1, Storage1MemStatus: 1, Storage2Status: 1, Storage2MemStatus: 1,
		CameraStatus:     [8]int{1, 1, 1, 1, 0, 0, 0, 0},
		MicrophoneStatus: [8]int{0, 0, 0, 0, 0, 0, 0, 0},
		IgnitionStatus:   1, EmergencyButton: 0, BatteryPercent: 40, LowBatteryThreshold: 7.0,
		MemoryPercent: 23, UpdateRateIgnitionOn: 5, UpdateRateIgnitionOff: 15, DigitalIOStatus: "0000", AnalogIOStatus: 0,
	}
	raw := c.BuildHealthStatus(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	got, err := DecodeHealthStatus(f)
	if err != nil {
		t.Fatalf("DecodeHealthStatus: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestEmergencyAlertRoundTrip(t *testing.T) {
	c := NewCodec()
	want := EmergencyAlert{
		IMEI: "864819050843380", PacketType: "NM", DateTime: "12032024112442", GPSValid: "A",
		Latitude: 17.478336666, LatDir: "N", Longitude: 78.570258333, LngDir: "E",
		AltitudeM: 578.956, SpeedKmh: 0, Distance: 0, Provider: "G", Vehicle: "AP36TS1234", ReplyNumber: "9898787654",
	}
	raw := c.BuildEmergencyAlert(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	if f.Token != "EPB" {
		t.Fatalf("expected token EPB, got %q", f.Token)
	}
	got, err := DecodeEmergencyAlert(f)
	if err != nil {
		t.Fatalf("DecodeEmergencyAlert: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestCANDataRoundTrip(t *testing.T) {
	c := NewCodec()
	want := CANData{
		Vehicle: "DL1PD8365", IMEI: "863418058197328", Date: "24102025", Time: "175758",
		Streams: []CANStream{
			{Label: "CAN1:", Values: []string{"5270.6", "0.0", "92.0", "100.0"}},
			{Label: "CAN2:", Values: []string{"0.0", "1.0", "0.0", "0.0"}},
		},
	}
	raw := c.BuildCANData(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	got, err := DecodeCANData(f)
	if err != nil {
		t.Fatalf("DecodeCANData: %v", err)
	}
	if got.Vehicle != want.Vehicle || len(got.Streams) != 2 {
		t.Fatalf("unexpected decode: %+v", got)
	}
	if got.Streams[0].Label != "CAN1:" || len(got.Streams[0].Values) != 4 || got.Streams[0].Values[0] != "5270.6" {
		t.Fatalf("unexpected stream 0: %+v", got.Streams[0])
	}
	if got.Streams[1].Label != "CAN2:" || len(got.Streams[1].Values) != 4 {
		t.Fatalf("unexpected stream 1: %+v", got.Streams[1])
	}
}

func TestAPCOnEveryStopRoundTrip(t *testing.T) {
	c := NewCodec()
	want := APCOnEveryStop{
		Vehicle: "DL3CBM9821", PacketStatus: "L", IMEI: "123456789012345", DateTime: "200728121324",
		Latitude: 17.7823411, LatDir: "N", Longitude: 78.3123123, LngDir: "E",
		StopName: "RGI Airport", RouteNumber: "13U", FrontIn: 12, FrontOut: 13, BackIn: 14, BackOut: 15,
	}
	raw := c.BuildAPCOnEveryStop(want)

	f, err := c.ParseVerify(raw)
	if err != nil {
		t.Fatalf("ParseVerify: %v", err)
	}
	got, err := DecodeAPCOnEveryStop(f)
	if err != nil {
		t.Fatalf("DecodeAPCOnEveryStop: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestConductorDutyUsesCorrectHeaders(t *testing.T) {
	c := NewCodec()
	start := c.BuildConductorDutyStart(ConductorDutyStart{})
	end := c.BuildConductorDutyEnd(ConductorDutyEnd{})

	sf, err := Parse(start)
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	if sf.Token != "147" {
		t.Fatalf("expected start token 147, got %q", sf.Token)
	}
	ef, err := Parse(end)
	if err != nil {
		t.Fatalf("parse end: %v", err)
	}
	if ef.Token != "148" {
		t.Fatalf("expected end token 148, got %q", ef.Token)
	}
}
