package n9mserver

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"mediamtx-console/vendorclients/n9m"
)

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) { l.t.Logf(format, args...) }

// listenLoopback opens a TCP listener on 127.0.0.1:0 and returns it plus its
// address string for dialing.
func listenLoopback(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln, ln.Addr().String()
}

// fakeDevice dials signalAddr, performs the CONNECT handshake, answers
// KEEPALIVE and REQUESTALIVEVIDEO, and on request dials mediaAddr to bind a
// media stream and pushes a few fake video frames.
type fakeDevice struct {
	t          *testing.T
	dsno       string
	signalConn *n9m.Conn
	mediaAddr  string
	session    string
}

func startFakeDevice(t *testing.T, signalAddr, mediaAddr, dsno string) *fakeDevice {
	t.Helper()
	nc, err := net.Dial("tcp", signalAddr)
	if err != nil {
		t.Fatalf("dial signaling: %v", err)
	}
	conn := n9m.NewConn(nc)
	conn.Start()

	d := &fakeDevice{t: t, dsno: dsno, signalConn: conn, mediaAddr: mediaAddr, session: n9m.NewSessionID()}

	resp, err := conn.Connect(d.session, n9m.ConnectParams{
		NET: 2, DevName: "X5AHD", DSNO: dsno,
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("device CONNECT: %v", err)
	}
	if resp.ErrorCode != 0 {
		t.Fatalf("device CONNECT rejected: %+v", resp)
	}

	go d.serve()
	return d
}

func (d *fakeDevice) serve() {
	for env := range d.signalConn.Notifications {
		switch env.Module + "/" + env.Operation {
		case n9m.ModuleMediaStream + "/" + n9m.OpRequestAliveVideo:
			var p n9m.RequestAliveVideoParams
			_ = json.Unmarshal(env.Parameter, &p)
			go d.openMediaChannel(p.StreamName)
			_ = d.signalConn.SendResponse(env.Module, env.Operation, env.Session, n9m.RequestAliveVideoResponse{
				ErrorResponse: n9m.ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"},
				StreamName:    p.StreamName,
				StreamType:    p.StreamType,
			})
		}
	}
}

func (d *fakeDevice) openMediaChannel(streamName string) {
	nc, err := net.Dial("tcp", d.mediaAddr)
	if err != nil {
		d.t.Errorf("device dial media: %v", err)
		return
	}
	mc := n9m.NewConn(nc)
	mc.Start()

	if err := mc.CreateStream(d.session, n9m.CreateStreamParams{
		StreamName: streamName,
		DSNO:       d.dsno,
	}, 5*time.Second); err != nil {
		d.t.Errorf("device CREATESTREAM: %v", err)
		return
	}

	for i := 0; i < 3; i++ {
		payload := []byte{byte(i), byte(i), byte(i)}
		if err := mc.SendMediaFrame(n9m.Header{Version: 1, PayloadType: n9m.PayloadLiveVideo}, payload); err != nil {
			d.t.Errorf("device send media frame: %v", err)
			return
		}
	}
}

func TestServerHandshakeAndLiveVideoHandoff(t *testing.T) {
	signalLn, signalAddr := listenLoopback(t)
	defer signalLn.Close()
	mediaLn, mediaAddr := listenLoopback(t)
	defer mediaLn.Close()

	srv := NewServer(testLogger{t})
	go srv.ServeSignaling(signalLn)
	go srv.ServeMedia(mediaLn)

	dsno := "00600052B8"
	dev := startFakeDevice(t, signalAddr, mediaAddr, dsno)
	defer dev.signalConn.Close()

	// Wait for the device to appear in the registry (handshake is async).
	deadline := time.After(2 * time.Second)
	for {
		if _, ok := srv.Registry.Get(dsno); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for device to register")
		case <-time.After(10 * time.Millisecond):
		}
	}

	mediaConn, err := srv.RequestLiveVideo(dsno, "channel1-sub", n9m.RequestAliveVideoParams{
		StreamType: 0,
		Channel:    1,
	}, 3*time.Second)
	if err != nil {
		t.Fatalf("RequestLiveVideo: %v", err)
	}
	defer mediaConn.Close()

	reader := NewMediaFrameReader(mediaConn, n9m.PayloadLiveVideo)
	buf := make([]byte, 3)
	for i := 0; i < 3; i++ {
		n, err := reader.Read(buf)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if n != 3 || buf[0] != byte(i) {
			t.Fatalf("frame %d mismatch: got %v", i, buf[:n])
		}
	}
}

func TestRequestLiveVideoUnknownDevice(t *testing.T) {
	srv := NewServer(testLogger{t})
	_, err := srv.RequestLiveVideo("no-such-device", "s1", n9m.RequestAliveVideoParams{}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
	if _, ok := err.(*ErrDeviceNotConnected); !ok {
		t.Fatalf("expected *ErrDeviceNotConnected, got %T: %v", err, err)
	}
}
