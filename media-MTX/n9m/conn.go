package n9m

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// Conn wraps a single N9M TCP connection (signaling or media). It can be
// used from either side: as a client emulating a device pushing data to a
// server, or as a server-side handler for an accepted device connection.
//
// Request/response correlation follows the protocol's own convention: there
// is no numeric request ID, so replies are matched by MODULE+OPERATION.
// Frames that don't match a pending request (device-initiated pushes like
// alarms, IO status, or a mirrored KEEPALIVE from the other side) are
// delivered on Notifications. Non-command frames (audio/video/etc, see
// PayloadType) are delivered on Media.
type Conn struct {
	nc net.Conn

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan Envelope

	Notifications chan Envelope
	Media         chan Frame

	closeOnce sync.Once
	done      chan struct{}
	readErr   error
	readErrMu sync.Mutex
}

// NewConn wraps an established net.Conn (dialed or accepted). Call Start to
// begin the read loop.
func NewConn(nc net.Conn) *Conn {
	return &Conn{
		nc:            nc,
		pending:       make(map[string]chan Envelope),
		Notifications: make(chan Envelope, 64),
		Media:         make(chan Frame, 64),
		done:          make(chan struct{}),
	}
}

// Start launches the background read loop. It returns immediately; read
// errors surface via Err() after Notifications/Media are closed.
func (c *Conn) Start() {
	go c.readLoop()
}

// Err returns the error that terminated the read loop, if any (nil while
// still running or if the connection was closed cleanly via Close).
func (c *Conn) Err() error {
	c.readErrMu.Lock()
	defer c.readErrMu.Unlock()
	return c.readErr
}

// Close closes the underlying connection and stops the read loop.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.nc.Close()
	})
	return err
}

func (c *Conn) readLoop() {
	defer func() {
		c.pendingMu.Lock()
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = nil
		c.pendingMu.Unlock()
		close(c.Notifications)
		close(c.Media)
	}()

	for {
		frame, err := ReadFrame(c.nc)
		if err != nil {
			c.readErrMu.Lock()
			c.readErr = err
			c.readErrMu.Unlock()
			return
		}

		if frame.Header.PayloadType != PayloadCommand {
			select {
			case c.Media <- frame:
			case <-c.done:
				return
			}
			continue
		}

		var env Envelope
		if err := json.Unmarshal(frame.Payload, &env); err != nil {
			// Not valid JSON on a command-typed frame; surface as a
			// best-effort notification so callers can log it, then continue.
			select {
			case c.Notifications <- Envelope{Module: "UNKNOWN", Operation: "DECODE_ERROR", Response: json.RawMessage(fmt.Sprintf("%q", err.Error()))}:
			case <-c.done:
				return
			}
			continue
		}

		key := env.key()
		c.pendingMu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.pendingMu.Unlock()

		if ok {
			ch <- env
			continue
		}

		select {
		case c.Notifications <- env:
		case <-c.done:
			return
		}
	}
}

// SendCommand writes a fire-and-forget PayloadCommand frame. parameter is
// marshaled into the PARAMETER field; pass nil to omit it (e.g. KEEPALIVE).
func (c *Conn) SendCommand(module, operation, session string, parameter any) error {
	env := Envelope{Module: module, Operation: operation, Session: session}
	if parameter != nil {
		raw, err := json.Marshal(parameter)
		if err != nil {
			return fmt.Errorf("n9m: encode parameter: %w", err)
		}
		env.Parameter = raw
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("n9m: encode envelope: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.nc, Header{Version: 1, PayloadType: PayloadCommand}, payload)
}

// SendResponse writes a fire-and-forget PayloadCommand frame carrying a
// RESPONSE object (used when acting as the server side replying to a
// request).
func (c *Conn) SendResponse(module, operation, session string, response any) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("n9m: encode response: %w", err)
	}
	env := Envelope{Module: module, Operation: operation, Session: session, Response: raw}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("n9m: encode envelope: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.nc, Header{Version: 1, PayloadType: PayloadCommand}, payload)
}

// SendMediaFrame writes a raw, non-JSON frame (audio/video/snapshot/etc).
func (c *Conn) SendMediaFrame(h Header, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.nc, h, payload)
}

// ErrTimeout is returned by Request when no matching reply arrives in time.
type ErrTimeout struct{ Module, Operation string }

func (e *ErrTimeout) Error() string {
	return fmt.Sprintf("n9m: timed out waiting for %s/%s response", e.Module, e.Operation)
}

// ErrClosed is returned by Request when the connection's read loop exits
// while a request is outstanding.
type ErrClosed struct{ Module, Operation string }

func (e *ErrClosed) Error() string {
	return fmt.Sprintf("n9m: connection closed waiting for %s/%s response", e.Module, e.Operation)
}

// Request sends a command and blocks for the correlated reply (matched by
// MODULE+OPERATION — see the Conn doc comment for the correlation caveat).
// Only one outstanding Request per (module, operation) pair is supported at
// a time; a second concurrent call with the same key will overwrite the
// first's waiter.
func (c *Conn) Request(module, operation, session string, parameter any, timeout time.Duration) (Envelope, error) {
	key := module + "/" + operation
	ch := make(chan Envelope, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pendingMu.Unlock()
		return Envelope{}, &ErrClosed{module, operation}
	}
	c.pending[key] = ch
	c.pendingMu.Unlock()

	if err := c.SendCommand(module, operation, session, parameter); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return Envelope{}, err
	}

	select {
	case env, ok := <-ch:
		if !ok {
			return Envelope{}, &ErrClosed{module, operation}
		}
		return env, nil
	case <-time.After(timeout):
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return Envelope{}, &ErrTimeout{module, operation}
	case <-c.done:
		return Envelope{}, &ErrClosed{module, operation}
	}
}

// NewSessionID generates a random session identifier suitable for the
// protocol's SESSION field (the doc uses a UUID-like string but does not
// mandate a specific format).
func NewSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const defaultTimeout = 10 * time.Second

// --- Typed helpers for the common commands ---

// Connect performs the CERTIFICATE/CONNECT handshake and returns the
// server's parsed response. Use NewSessionID() to generate sessionID.
func (c *Conn) Connect(sessionID string, p ConnectParams, timeout time.Duration) (ConnectResponse, error) {
	env, err := c.Request(ModuleCertificate, OpConnect, sessionID, p, timeout)
	if err != nil {
		return ConnectResponse{}, err
	}
	var resp ConnectResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return ConnectResponse{}, fmt.Errorf("n9m: decode CONNECT response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return resp, fmt.Errorf("n9m: CONNECT failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return resp, nil
}

// KeepAlive sends a heartbeat and waits for the mirrored reply.
func (c *Conn) KeepAlive(sessionID string, timeout time.Duration) error {
	_, err := c.Request(ModuleCertificate, OpKeepAlive, sessionID, nil, timeout)
	return err
}

// StartHeartbeatLoop sends KeepAlive on the given interval until stop is
// closed or a heartbeat fails onErr times consecutively (onErr may be nil).
// The doc recommends a 45s interval with disconnect after 5 consecutive
// misses.
func (c *Conn) StartHeartbeatLoop(sessionID string, interval time.Duration, onErr func(error)) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-c.done:
				return
			case <-ticker.C:
				if err := c.KeepAlive(sessionID, interval/2); err != nil && onErr != nil {
					onErr(err)
				}
			}
		}
	}()
	return func() { close(done) }
}

// CreateStream binds a freshly-dialed media connection to a signaling
// session via CERTIFICATE/CREATESTREAM.
func (c *Conn) CreateStream(sessionID string, p CreateStreamParams, timeout time.Duration) error {
	env, err := c.Request(ModuleCertificate, OpCreateStream, sessionID, p, timeout)
	if err != nil {
		return err
	}
	var resp ErrorResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return fmt.Errorf("n9m: decode CREATESTREAM response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("n9m: CREATESTREAM failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return nil
}

// NotifyMediaTaskStart sends MEDIASTREAMMODEL/MEDIATASKSTART (fire-and-forget;
// the doc does not define a reply for this notification).
func (c *Conn) NotifyMediaTaskStart(sessionID string, p MediaTaskStartParams) error {
	return c.SendCommand(ModuleMediaStream, OpMediaTaskStart, sessionID, p)
}

// NotifyMediaTaskStop sends MEDIASTREAMMODEL/MEDIATASKSTOP.
func (c *Conn) NotifyMediaTaskStop(sessionID string, p MediaTaskStopParams) error {
	return c.SendCommand(ModuleMediaStream, OpMediaTaskStop, sessionID, p)
}

// RequestAliveVideo requests a live-preview stream from the device this Conn
// is talking to.
func (c *Conn) RequestAliveVideo(sessionID string, p RequestAliveVideoParams, timeout time.Duration) (RequestAliveVideoResponse, error) {
	env, err := c.Request(ModuleMediaStream, OpRequestAliveVideo, sessionID, p, timeout)
	if err != nil {
		return RequestAliveVideoResponse{}, err
	}
	var resp RequestAliveVideoResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return RequestAliveVideoResponse{}, fmt.Errorf("n9m: decode REQUESTALIVEVIDEO response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return resp, fmt.Errorf("n9m: REQUESTALIVEVIDEO failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return resp, nil
}

// ControlStream sends MEDIASTREAMMODEL/CONTROLSTREAM (stop/resume/pause/
// switch-stream/audio-toggle/frame-rate) for an in-progress live task.
func (c *Conn) ControlStream(sessionID string, p ControlStreamParams, timeout time.Duration) error {
	env, err := c.Request(ModuleMediaStream, OpControlStream, sessionID, p, timeout)
	if err != nil {
		return err
	}
	var resp ErrorResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return fmt.Errorf("n9m: decode CONTROLSTREAM response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("n9m: CONTROLSTREAM failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return nil
}

// GetIFrame requests an immediate I-frame on the given channel bitmask/
// stream type (AVSM/GETIFRAME).
func (c *Conn) GetIFrame(sessionID string, p GetIFrameParams, timeout time.Duration) error {
	env, err := c.Request(ModuleAVSM, OpGetIFrame, sessionID, p, timeout)
	if err != nil {
		return err
	}
	var resp ErrorResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return fmt.Errorf("n9m: decode GETIFRAME response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("n9m: GETIFRAME failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return nil
}

// RequestTalk starts a two-way intercom session.
func (c *Conn) RequestTalk(sessionID string, p RequestTalkParams, timeout time.Duration) error {
	env, err := c.Request(ModuleMediaStream, OpRequestTalk, sessionID, p, timeout)
	if err != nil {
		return err
	}
	var resp ErrorResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return fmt.Errorf("n9m: decode REQUESTTALK response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("n9m: REQUESTTALK failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return nil
}

// ControlTalk stops an in-progress intercom session.
func (c *Conn) ControlTalk(sessionID string, p ControlTalkParams, timeout time.Duration) error {
	env, err := c.Request(ModuleMediaStream, OpControlTalk, sessionID, p, timeout)
	if err != nil {
		return err
	}
	var resp ErrorResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return fmt.Errorf("n9m: decode CONTROLTALK response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("n9m: CONTROLTALK failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return nil
}
