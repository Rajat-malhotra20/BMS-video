package n9mserver

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"mediamtx-console/vendorclients/n9m"
)

// handshakeTimeout bounds how long a freshly-accepted connection has to send
// its first command (CONNECT on the signaling port, CREATESTREAM on the
// media port) before it's dropped.
const handshakeTimeout = 15 * time.Second

// Logger is the minimal logging interface Server needs; *log.Logger and
// testing.T-wrapping adapters both satisfy it.
type Logger interface {
	Printf(format string, args ...any)
}

// Server accepts device-initiated N9M signaling and media connections. Per
// the protocol, devices dial the signaling port first, authenticate with
// CERTIFICATE/CONNECT, and later open one media connection per active
// stream, binding it with CERTIFICATE/CREATESTREAM.
type Server struct {
	Registry *Registry
	Logger   Logger

	// OnAlarm, if set, is called (from the connection's own goroutine, so it
	// must not block) whenever a device reports EVEM/SENDALARMINFO, after the
	// protocol-mandated ack has already been sent.
	OnAlarm func(dsno string, alarm json.RawMessage)
}

// NewServer returns a Server with a fresh Registry.
func NewServer(logger Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{Registry: NewRegistry(), Logger: logger}
}

// ServeSignaling accepts connections on ln, treating each as a device
// signaling channel. It blocks until ln.Accept returns a non-temporary
// error (e.g. the listener was closed).
func (s *Server) ServeSignaling(ln net.Listener) error {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleSignalingConn(nc)
	}
}

// ServeMedia accepts connections on ln, treating each as a device media
// channel (audio/video/etc for a stream previously requested over
// signaling).
func (s *Server) ServeMedia(ln net.Listener) error {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleMediaConn(nc)
	}
}

func (s *Server) handleSignalingConn(nc net.Conn) {
	conn := n9m.NewConn(nc)
	conn.Start()
	remote := nc.RemoteAddr().String()

	env, ok := s.awaitFirst(conn, handshakeTimeout)
	if !ok {
		s.Logger.Printf("n9mserver: %s: handshake timed out or connection closed", remote)
		conn.Close()
		return
	}
	if env.Module != n9m.ModuleCertificate || env.Operation != n9m.OpConnect {
		s.Logger.Printf("n9mserver: %s: expected CERTIFICATE/CONNECT, got %s/%s", remote, env.Module, env.Operation)
		conn.Close()
		return
	}

	var params n9m.ConnectParams
	if err := json.Unmarshal(env.Parameter, &params); err != nil {
		s.Logger.Printf("n9mserver: %s: decode CONNECT parameter: %v", remote, err)
		conn.Close()
		return
	}
	if params.DSNO == "" {
		s.Logger.Printf("n9mserver: %s: CONNECT missing DSNO, rejecting", remote)
		_ = conn.SendResponse(env.Module, env.Operation, env.Session, n9m.ConnectResponse{
			ErrorResponse: n9m.ErrorResponse{ErrorCode: 5, ErrorCause: "missing DSNO"},
		})
		conn.Close()
		return
	}

	session := &DeviceSession{
		DSNO:        params.DSNO,
		Session:     env.Session,
		Conn:        conn,
		Info:        params,
		ConnectedAt: time.Now(),
	}
	s.Registry.Put(session)
	s.Logger.Printf("n9mserver: device %s connected from %s (session=%s)", params.DSNO, remote, env.Session)

	if err := conn.SendResponse(env.Module, env.Operation, env.Session, n9m.ConnectResponse{
		ErrorResponse: n9m.ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"},
		PRO:           "1.0.4",
		MaskCmd:       1,
	}); err != nil {
		s.Logger.Printf("n9mserver: %s: send CONNECT response: %v", remote, err)
		s.Registry.Remove(params.DSNO, conn)
		conn.Close()
		return
	}

	defer func() {
		s.Registry.Remove(params.DSNO, conn)
		conn.Close()
		s.Logger.Printf("n9mserver: device %s disconnected", params.DSNO)
	}()

	for env := range conn.Notifications {
		s.dispatchSignalingEnvelope(conn, params.DSNO, env)
	}
}

func (s *Server) dispatchSignalingEnvelope(conn *n9m.Conn, dsno string, env n9m.Envelope) {
	switch env.Module + "/" + env.Operation {
	case n9m.ModuleCertificate + "/" + n9m.OpKeepAlive:
		if err := conn.SendCommand(env.Module, env.Operation, env.Session, nil); err != nil {
			s.Logger.Printf("n9mserver: %s: mirror KEEPALIVE: %v", dsno, err)
		}
	case n9m.ModuleEvent + "/" + n9m.OpSendAlarmInfo:
		var common n9m.AlarmCommonParams
		if err := json.Unmarshal(env.Parameter, &common); err != nil {
			s.Logger.Printf("n9mserver: %s: decode alarm: %v", dsno, err)
			return
		}
		if err := conn.SendResponse(env.Module, env.Operation, env.Session, common.AckFrom()); err != nil {
			s.Logger.Printf("n9mserver: %s: ack alarm: %v", dsno, err)
		}
		if s.OnAlarm != nil {
			s.OnAlarm(dsno, env.Parameter)
		}
	default:
		s.Logger.Printf("n9mserver: %s: unhandled %s/%s", dsno, env.Module, env.Operation)
	}
}

func (s *Server) handleMediaConn(nc net.Conn) {
	conn := n9m.NewConn(nc)
	conn.Start()
	remote := nc.RemoteAddr().String()

	env, ok := s.awaitFirst(conn, handshakeTimeout)
	if !ok {
		s.Logger.Printf("n9mserver: media %s: handshake timed out or connection closed", remote)
		conn.Close()
		return
	}
	if env.Module != n9m.ModuleCertificate || env.Operation != n9m.OpCreateStream {
		s.Logger.Printf("n9mserver: media %s: expected CERTIFICATE/CREATESTREAM, got %s/%s", remote, env.Module, env.Operation)
		conn.Close()
		return
	}

	var params n9m.CreateStreamParams
	if err := json.Unmarshal(env.Parameter, &params); err != nil {
		s.Logger.Printf("n9mserver: media %s: decode CREATESTREAM parameter: %v", remote, err)
		conn.Close()
		return
	}
	if params.StreamName == "" {
		_ = conn.SendResponse(env.Module, env.Operation, env.Session, n9m.ErrorResponse{ErrorCode: 42, ErrorCause: "missing STREAMNAME"})
		conn.Close()
		return
	}

	if err := conn.SendResponse(env.Module, env.Operation, env.Session, n9m.ErrorResponse{ErrorCode: 0, ErrorCause: "SUCCESS"}); err != nil {
		s.Logger.Printf("n9mserver: media %s: send CREATESTREAM response: %v", remote, err)
		conn.Close()
		return
	}

	// Hand the connected media channel off to whoever called
	// RequestLiveVideo and is waiting on this (DSNO, StreamName) pair. If
	// nobody is waiting (e.g. the request timed out just before the device
	// dialed in), there's nothing useful to do with an unclaimed media
	// channel, so close it.
	if !s.Registry.handoffMediaConn(params.DSNO, params.StreamName, conn) {
		s.Logger.Printf("n9mserver: media %s: no waiter for %s/%s, closing", remote, params.DSNO, params.StreamName)
		conn.Close()
	}
}

// awaitFirst waits for the first Notification (i.e. the peer-initiated
// request that hasn't matched an outstanding local Request) or reports ok =
// false on timeout / connection close.
func (s *Server) awaitFirst(conn *n9m.Conn, timeout time.Duration) (n9m.Envelope, bool) {
	select {
	case env, ok := <-conn.Notifications:
		return env, ok
	case <-time.After(timeout):
		return n9m.Envelope{}, false
	}
}

// RequestLiveVideo asks a connected device to start a live-video stream and
// blocks until the device's media connection for it arrives (or timeout
// elapses). The returned *n9m.Conn's Media channel yields the raw
// PayloadLiveVideo frames; the caller is responsible for closing it (e.g.
// via StopLiveVideo or directly) once done.
func (s *Server) RequestLiveVideo(dsno, streamName string, p n9m.RequestAliveVideoParams, timeout time.Duration) (*n9m.Conn, error) {
	dev, ok := s.Registry.Get(dsno)
	if !ok {
		return nil, &ErrDeviceNotConnected{DSNO: dsno}
	}
	p.StreamName = streamName

	resultCh := make(chan struct {
		conn *n9m.Conn
		err  error
	}, 1)

	go func() {
		conn, err := s.Registry.awaitMediaConn(dsno, streamName, timeout)
		resultCh <- struct {
			conn *n9m.Conn
			err  error
		}{conn, err}
	}()

	if _, err := dev.Conn.RequestAliveVideo(dev.Session, p, timeout); err != nil {
		// Drain the awaiting goroutine so it doesn't leak; it will time out
		// or be handed a connection that we then discard.
		go func() {
			r := <-resultCh
			if r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, fmt.Errorf("n9mserver: REQUESTALIVEVIDEO to %s failed: %w", dsno, err)
	}

	r := <-resultCh
	if r.err != nil {
		return nil, r.err
	}
	return r.conn, nil
}

// StopLiveVideo sends CONTROLSTREAM(stop) on the device's signaling channel
// and closes the given media connection.
func (s *Server) StopLiveVideo(dsno, streamName string, ssrc int, mediaConn *n9m.Conn) error {
	dev, ok := s.Registry.Get(dsno)
	if !ok {
		if mediaConn != nil {
			mediaConn.Close()
		}
		return &ErrDeviceNotConnected{DSNO: dsno}
	}
	err := dev.Conn.ControlStream(dev.Session, n9m.ControlStreamParams{
		SSRC:       ssrc,
		StreamName: streamName,
		Cmd:        n9m.StreamCmdStop,
	}, 5*time.Second)
	if mediaConn != nil {
		mediaConn.Close()
	}
	return err
}
