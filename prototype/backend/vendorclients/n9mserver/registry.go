// Package n9mserver implements the server side of the Chemito N9M protocol:
// it accepts device-initiated signaling connections (CERTIFICATE/CONNECT
// handshake, KEEPALIVE, alarm acks) and device-initiated media connections
// (CERTIFICATE/CREATESTREAM), and lets a caller request a live-video media
// stream from a connected device by its serial number (DSNO).
package n9mserver

import (
	"sync"
	"time"

	"mediamtx-console/vendorclients/n9m"
)

// DeviceSession is one connected device's signaling channel plus metadata
// from its CONNECT handshake.
type DeviceSession struct {
	DSNO        string
	Session     string
	Conn        *n9m.Conn
	Info        n9m.ConnectParams
	ConnectedAt time.Time
}

// mediaWaitKey identifies an in-flight "waiting for the device to open a
// media connection for this stream name" request.
type mediaWaitKey struct {
	dsno       string
	streamName string
}

// Registry tracks connected devices and pending media-channel handoffs.
type Registry struct {
	mu           sync.Mutex
	devices      map[string]*DeviceSession // by DSNO
	mediaWaiters map[mediaWaitKey]chan *n9m.Conn
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		devices:      make(map[string]*DeviceSession),
		mediaWaiters: make(map[mediaWaitKey]chan *n9m.Conn),
	}
}

// Put registers (or replaces) a device's signaling session.
func (r *Registry) Put(d *DeviceSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.devices[d.DSNO] = d
}

// Remove drops a device's signaling session if it's still the one
// registered under dsno (guards against a stale close removing a newer
// reconnection).
func (r *Registry) Remove(dsno string, conn *n9m.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.devices[dsno]; ok && d.Conn == conn {
		delete(r.devices, dsno)
	}
}

// Get looks up a connected device by serial number.
func (r *Registry) Get(dsno string) (*DeviceSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[dsno]
	return d, ok
}

// List returns a snapshot of all connected devices.
func (r *Registry) List() []*DeviceSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*DeviceSession, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, d)
	}
	return out
}

// awaitMediaConn registers a waiter for (dsno, streamName)'s media
// connection and blocks until handoffMediaConn delivers one or timeout
// elapses.
func (r *Registry) awaitMediaConn(dsno, streamName string, timeout time.Duration) (*n9m.Conn, error) {
	key := mediaWaitKey{dsno, streamName}
	ch := make(chan *n9m.Conn, 1)

	r.mu.Lock()
	r.mediaWaiters[key] = ch
	r.mu.Unlock()

	select {
	case conn := <-ch:
		return conn, nil
	case <-time.After(timeout):
		r.mu.Lock()
		delete(r.mediaWaiters, key)
		r.mu.Unlock()
		return nil, &ErrMediaTimeout{DSNO: dsno, StreamName: streamName}
	}
}

// handoffMediaConn delivers a newly-accepted media connection to a waiter
// registered under (dsno, streamName), if any. It reports whether a waiter
// was found.
func (r *Registry) handoffMediaConn(dsno, streamName string, conn *n9m.Conn) bool {
	key := mediaWaitKey{dsno, streamName}
	r.mu.Lock()
	ch, ok := r.mediaWaiters[key]
	if ok {
		delete(r.mediaWaiters, key)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	ch <- conn
	return true
}

// ErrMediaTimeout is returned when a device doesn't open the expected media
// connection in time after a live-video request.
type ErrMediaTimeout struct {
	DSNO       string
	StreamName string
}

func (e *ErrMediaTimeout) Error() string {
	return "n9mserver: timed out waiting for device " + e.DSNO + " to open media channel " + e.StreamName
}

// ErrDeviceNotConnected is returned when an operation targets a DSNO with no
// active signaling session.
type ErrDeviceNotConnected struct{ DSNO string }

func (e *ErrDeviceNotConnected) Error() string {
	return "n9mserver: device " + e.DSNO + " is not connected"
}
