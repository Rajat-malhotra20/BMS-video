package t808

import (
	"reflect"
	"testing"
)

func TestRegisterRequestRoundTrip(t *testing.T) {
	want := RegisterRequest{
		ProvinceID: 1, CityID: 16, ManufacturerID: "24373", TerminalID: "CHEMITO",
		PlateColor: PlateColorNull, VehicleIDOrPlate: "MH06AF4014",
	}
	body, err := BuildRegisterRequest(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeRegisterRequest(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestRegisterResponseRoundTrip(t *testing.T) {
	want := RegisterResponse{AnsweringSerial: 6, Result: RegisterResultSuccess, VerificationCode: "MH06AF4014-1234567890"}
	body, err := BuildRegisterResponse(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeRegisterResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestRegisterResponseFailureHasNoVerificationCode(t *testing.T) {
	want := RegisterResponse{AnsweringSerial: 6, Result: RegisterResultNoVehicleData}
	body, _ := BuildRegisterResponse(want)
	got, err := DecodeRegisterResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.VerificationCode != "" {
		t.Fatalf("expected empty verification code on failure, got %q", got.VerificationCode)
	}
}

func TestGeneralResponseRoundTrip(t *testing.T) {
	want := GeneralResponse{AnsweringSerial: 1, AnsweringID: 0x0102, Result: GeneralResultSuccess}
	body := BuildGeneralResponse(want)
	got, err := DecodeGeneralResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestLocationReportRoundTrip(t *testing.T) {
	want := LocationReport{
		AlarmFlags: AlarmEmergency | AlarmOverSpeed,
		Status:     StatusACCOn | StatusPositioned,
		Latitude:   19973921, Longitude: 73805160,
		Elevation: 610, Speed: 0, Direction: 247,
		Time: BCDDateTime{Year: 20, Month: 4, Day: 14, Hour: 15, Minute: 1, Second: 32},
		Extensions: []LocationExtension{
			{ID: ExtMileage, Data: []byte{0x00, 0x00, 0x00, 0x00}},
			{ID: ExtSignalStrength, Data: []byte{0x03}},
			{ID: ExtSatelliteCount, Data: []byte{0x0C}},
			{ID: ExtAlertPIS, Data: EncodeAlertPISStatus(AlertPISStatus{TamperAlert: true, PIS2RDU: true})},
		},
	}
	body, err := BuildLocationReport(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeLocationReport(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
	if got.LatitudeDeg() != 19.973921 {
		t.Fatalf("unexpected LatitudeDeg: %v", got.LatitudeDeg())
	}
	ext, ok := got.Find(ExtAlertPIS)
	if !ok {
		t.Fatal("expected 0xEA extension present")
	}
	pis := DecodeAlertPISStatus(ext.Data)
	if !pis.TamperAlert || !pis.PIS2RDU || pis.BDCAlert || pis.PIS1FDU {
		t.Fatalf("unexpected PIS decode: %+v", pis)
	}
}

func TestAttendanceRoundTrip(t *testing.T) {
	want := AttendanceRequest{
		RouteNumber: 0x2D, EmployeeID: "eb0004f6",
		Time: BCDDateTime{Year: 20, Month: 4, Day: 15, Hour: 10, Minute: 46, Second: 13},
		Type: AttendanceSignOut, Method: AttendanceMethodEmployeeCard, Password: "123456",
	}
	body, err := BuildAttendanceRequest(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeAttendanceRequest(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestAttendanceResponseRoundTrip(t *testing.T) {
	want := AttendanceResponse{
		Response:       AttendanceRespCheckIn,
		ResponseTime:   BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 14, Minute: 10, Second: 24},
		ResponseString: "NARESH",
	}
	body, err := BuildAttendanceResponse(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeAttendanceResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestTextMessageRoundTrip(t *testing.T) {
	want := TextMessage{Flags: TextFlagOBUDisplay | TextFlagOBUTTSBroadcast, Text: "Naresh you sign in on XX4014 successfully"}
	body, err := BuildTextMessage(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeTextMessage(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestOperationRequestRoundTrip(t *testing.T) {
	want := OperationRequest{
		RouteNumber: 1, EmployeeID: "eb0004f6", RequestCode: OpSchedulingRequest,
		Time: BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 10, Minute: 30, Second: 0},
	}
	body, err := BuildOperationRequest(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeOperationRequest(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func testQueue() DepartureQueue {
	return DepartureQueue{
		RouteNumber: 100, RoadSign: "45", TripNumber: "6", BusNumber: "XX4014",
		BusinessType: BusinessDownwardRun, ScheduleType: ScheduleWholeRoad,
		BeginTime:          BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 12, Minute: 0, Second: 0},
		EndTime:            BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 13, Minute: 0, Second: 0},
		StartStationNumber: 1, StartStationName: "I.S.B.T.-43",
		TerminationStationNumber: 18, TerminationStationName: "I.T. Park",
	}
}

func TestOperationResponseRoundTrip(t *testing.T) {
	want := OperationResponse{
		AnsweringSerial: 1, Agreed: true,
		RespondTime:    BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 10, Minute: 30, Second: 0},
		Queue:          testQueue(),
		AdditionalInfo: `Please Start at 12 Hours {"TS":3,"OTS":1,"OT":60}`,
	}
	body, err := BuildOperationResponse(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeOperationResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestDrivingPlanRoundTrip(t *testing.T) {
	want := DrivingPlanResponse{
		OperatingDate:  BCDDateTime{Year: 20, Month: 4, Day: 20},
		RunningTimes:   2,
		RoadSign:       "45",
		StartTime:      BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 6, Minute: 0, Second: 0},
		EndTime:        BCDDateTime{Year: 20, Month: 4, Day: 20, Hour: 22, Minute: 0, Second: 0},
		Plans:          []DepartureQueue{testQueue(), testQueue()},
		AdditionalInfo: "notes",
	}
	body, err := BuildDrivingPlanResponse(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeDrivingPlanResponse(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestDrivingPlanRequestRoundTrip(t *testing.T) {
	want := DrivingPlanRequest{OperatingDate: BCDDateTime{Year: 20, Month: 4, Day: 20}, EmployeeID: "eb0004f6"}
	body, err := BuildDrivingPlanRequest(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeDrivingPlanRequest(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestCANUploadRoundTrip(t *testing.T) {
	want := CANUpload{
		ReceivingTime: BCDTime5{Hour: 10, Minute: 20, Second: 30, Millis: 500},
		Items: []CANItem{
			{ID: 0x0CF00400, Data: [8]byte{0, 1, 2, 3, 4, 5, 6, 7}},
			{ID: 0x18FEF100, Data: [8]byte{0, 1, 2, 3, 4, 5, 6, 7}},
		},
	}
	body, err := BuildCANUpload(want)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := DecodeCANUpload(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func TestAuthRequestRoundTrip(t *testing.T) {
	want := AuthRequest{VerificationCode: "MH06AF4014-1234567890"}
	body := BuildAuthRequest(want)
	got, err := DecodeAuthRequest(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("mismatch:\n want %+v\n  got %+v", want, got)
	}
}
