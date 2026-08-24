package n9m

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// fakeDevice reads requests off notifications and replies, emulating the
// device side of the protocol for a couple of commands.
func fakeDevice(t *testing.T, conn *Conn) {
	t.Helper()
	go func() {
		for env := range conn.Notifications {
			switch env.key() {
			case ModuleCertificate + "/" + OpConnect:
				resp := ConnectResponse{
					ErrorResponse: ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"},
					DevType:       1,
					PRO:           "1.0.4",
					MaskCmd:       1,
				}
				if err := conn.SendResponse(env.Module, env.Operation, env.Session, resp); err != nil {
					t.Errorf("device: send connect response: %v", err)
				}
			case ModuleMediaStream + "/" + OpRequestAliveVideo:
				var p RequestAliveVideoParams
				_ = json.Unmarshal(env.Parameter, &p)
				resp := RequestAliveVideoResponse{
					ErrorResponse: ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"},
					StreamName:    p.StreamName,
					StreamType:    p.StreamType,
				}
				if err := conn.SendResponse(env.Module, env.Operation, env.Session, resp); err != nil {
					t.Errorf("device: send requestalivevideo response: %v", err)
				}
			case ModuleCertificate + "/" + OpKeepAlive:
				if err := conn.SendCommand(env.Module, env.Operation, env.Session, nil); err != nil {
					t.Errorf("device: mirror keepalive: %v", err)
				}
			}
		}
	}()
}

func TestConnectAndRequestAliveVideoOverPipe(t *testing.T) {
	serverSide, deviceSide := net.Pipe()
	defer serverSide.Close()
	defer deviceSide.Close()

	server := NewConn(serverSide)
	device := NewConn(deviceSide)
	server.Start()
	device.Start()
	fakeDevice(t, device)

	session := NewSessionID()
	if len(session) == 0 {
		t.Fatal("expected non-empty session id")
	}

	connResp, err := server.Connect(session, ConnectParams{
		NET:     2,
		DevName: "X5AHD",
		DSNO:    "001A001B001C",
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if connResp.PRO != "1.0.4" {
		t.Fatalf("unexpected PRO: %q", connResp.PRO)
	}

	if err := server.KeepAlive(session, 2*time.Second); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}

	videoResp, err := server.RequestAliveVideo(session, RequestAliveVideoParams{
		StreamName: "channel1-sub",
		StreamType: 0,
		Channel:    1,
		IPAndPort:  "192.168.1.1:8002",
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("RequestAliveVideo: %v", err)
	}
	if videoResp.StreamName != "channel1-sub" {
		t.Fatalf("unexpected stream name: %q", videoResp.StreamName)
	}
}

func TestRequestTimesOutWithNoResponder(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	server := NewConn(a)
	other := NewConn(b)
	server.Start()
	other.Start()
	// Drain other's notifications without responding, so the request times out.
	go func() {
		for range other.Notifications {
		}
	}()

	_, err := server.Connect(NewSessionID(), ConnectParams{DevName: "x", DSNO: "y"}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if _, ok := err.(*ErrTimeout); !ok {
		t.Fatalf("expected *ErrTimeout, got %T: %v", err, err)
	}
}

func TestMediaFrameDeliveredSeparatelyFromCommands(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	receiver := NewConn(a)
	sender := NewConn(b)
	receiver.Start()
	sender.Start()

	videoBytes := []byte{0x00, 0x01, 0x02, 0x03}
	go func() {
		_ = sender.SendMediaFrame(Header{Version: 1, PayloadType: PayloadLiveVideo, SSRC: 3}, videoBytes)
	}()

	select {
	case frame := <-receiver.Media:
		if frame.Header.PayloadType != PayloadLiveVideo {
			t.Fatalf("expected PayloadLiveVideo, got %d", frame.Header.PayloadType)
		}
		if frame.Header.SSRC != 3 {
			t.Fatalf("expected SSRC 3, got %d", frame.Header.SSRC)
		}
		if string(frame.Payload) != string(videoBytes) {
			t.Fatalf("payload mismatch: %v", frame.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for media frame")
	}
}
