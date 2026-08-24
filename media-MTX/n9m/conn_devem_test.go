package n9m

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestQueryGeneralStatusAndControlDevCmdOverPipe(t *testing.T) {
	serverSide, deviceSide := net.Pipe()
	defer serverSide.Close()
	defer deviceSide.Close()

	server := NewConn(serverSide)
	device := NewConn(deviceSide)
	server.Start()
	device.Start()

	go func() {
		for env := range device.Notifications {
			switch env.key() {
			case ModuleDeviceEM + "/" + OpQueryGeneralStatus:
				resp := GeneralStatusResponse{
					GPSM:    &PositioningStatus{GS: 0, GT: 0, GPN: 12},
					VKS:     &CarKeyStatus{CKS: 1, HDK: 0},
					NetType: 2,
					Serial:  1,
				}
				_ = device.SendResponse(env.Module, env.Operation, env.Session, resp)
			case ModuleDeviceEM + "/" + OpSetControlDevCmd:
				var p SetControlDevCmdParams
				_ = json.Unmarshal(env.Parameter, &p)
				_ = device.SendResponse(env.Module, env.Operation, env.Session, SetControlDevCmdResponse{
					ErrorResponse: ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"},
					CmdType:       p.CmdType,
					Serial:        p.Serial,
				})
			}
		}
	}()

	status, err := server.QueryGeneralStatus("s1", GeneralStatusQuery{QMask: 160, Serial: 1}, 2*time.Second)
	if err != nil {
		t.Fatalf("QueryGeneralStatus: %v", err)
	}
	if status.GPSM == nil || status.GPSM.GPN != 12 {
		t.Fatalf("unexpected GPSM: %+v", status.GPSM)
	}
	if status.VKS == nil || status.VKS.CKS != 1 {
		t.Fatalf("unexpected VKS: %+v", status.VKS)
	}

	ctrlResp, err := server.SetControlDevCmd("s1", SetControlDevCmdParams{
		CmdType:  DevControlSleepPowerOff,
		Serial:   7,
		Continue: 30,
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("SetControlDevCmd: %v", err)
	}
	if ctrlResp.CmdType != DevControlSleepPowerOff || ctrlResp.Serial != 7 {
		t.Fatalf("unexpected ctrl response: %+v", ctrlResp)
	}
}

func TestPushIOStatusFireAndForget(t *testing.T) {
	serverSide, deviceSide := net.Pipe()
	defer serverSide.Close()
	defer deviceSide.Close()

	server := NewConn(serverSide)
	device := NewConn(deviceSide)
	server.Start()
	device.Start()

	done := make(chan IOStatusPayload, 1)
	go func() {
		for env := range server.Notifications {
			if env.key() == ModuleDeviceEM+"/"+OpUpdateIOStatus {
				var p IOStatusPayload
				_ = json.Unmarshal(env.Parameter, &p)
				done <- p
				return
			}
		}
	}()

	if err := device.PushIOStatus("s1", IOStatusPayload{
		IO:  []IOLine{{Name: "Sensor1", User: "IO1", S: 1, U: 2}},
		ACC: ACCStatus{A: 1},
		FN:  PulseInfo{N: 1000},
		FB:  0,
	}); err != nil {
		t.Fatalf("PushIOStatus: %v", err)
	}

	select {
	case p := <-done:
		if len(p.IO) != 1 || p.IO[0].Name != "Sensor1" || p.ACC.A != 1 {
			t.Fatalf("unexpected payload: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UPDATEIOSTATUS push")
	}
}
