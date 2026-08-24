// Package domain holds the vendor-agnostic types the media-MTX service
// works with. It is deliberately a separate, trimmed copy of the fleet
// API's domain package rather than a shared import: this service is the
// raw-packet side of the system and only ever produces one kind of source
// (an RTSP remux into MediaMTX), so it has no business carrying the
// embed/HLS/FLV shapes the fleet API needs — and a separate service that
// imports the caller it serves is a dependency pointing the wrong way.
package domain

import (
	"context"
	"fmt"
	"time"
)

// SourceKind exists for symmetry with the fleet API's wire format. This
// service only ever emits KindRTSP; the constant is here so the JSON it
// returns is self-describing rather than implying a kind by omission.
type SourceKind string

const KindRTSP SourceKind = "rtsp"

// LiveSource is what every adapter in this service resolves a request
// into: an upstream the ffmpeg supervisor can consume.
type LiveSource struct {
	Kind  SourceKind
	Remux RemuxInput

	// RestartBackoff, if nonzero, overrides the service's default initial
	// backoff for this job's Supervisor retries. Only the adapter knows
	// its own vendor's real session behavior.
	RestartBackoff time.Duration
}

// RemuxInput is anything the ffmpeg supervisor can consume as an upstream.
// Exactly one of URL / Run is set. HTTP vendors (Castmaster) give a stable
// URL — the service builds the remux job itself. Socket vendors (N9M) give
// a ready-built Run instead, because their upstream connection must be
// freshly renegotiated on every supervisor restart, not just the first
// start — only the adapter knows how to redo that negotiation.
type RemuxInput struct {
	URL string
	Run func(ctx context.Context) error
}

// StreamRequest is the single request shape this service works with.
// RTSPOut is filled in by the service (it owns the {bus}_{cam} naming
// convention) before calling the adapter, for the Run-based case above
// where the adapter needs it to build its own remux closure.
type StreamRequest struct {
	Bus, Vendor  string
	Cam          int
	Main, Audio  bool
	VendorParams map[string]string
	RTSPOut      string
}

// Camera is one selectable channel/vehicle a vendor can offer.
type Camera struct {
	VendorID, Label string
	VendorParams    map[string]string
	Online          bool
	Channels        int
}

// Adapter is the contract every vendor integration in this service
// implements. Adding one is: write adapters/<name>/adapter.go, register it
// in cmd/media-mtxd/main.go.
type Adapter interface {
	Name() string
	ResolveLiveSource(ctx context.Context, req StreamRequest) (LiveSource, error)
	ListCameras(ctx context.Context, vendorParams map[string]string) ([]Camera, error)
}

// VendorError normalizes a failure from any adapter so the HTTP layer can
// map it to a status code in one place instead of string-matching errors.
type VendorError struct {
	Vendor    string
	Op        string // what was being attempted, e.g. "login", "resolve live url"
	Code      string // vendor-agnostic reason, e.g. "device_offline", "not_implemented"
	Retryable bool
	Cause     error
}

func (e *VendorError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Vendor, e.Op, e.Cause)
	}
	return fmt.Sprintf("%s: %s: %s", e.Vendor, e.Op, e.Code)
}

func (e *VendorError) Unwrap() error { return e.Cause }

// WrapVendorErr wraps a raw vendor-client error with the vendor/op context
// needed for logging and status-code mapping. Returns nil if err is nil.
func WrapVendorErr(vendor, op string, err error) error {
	if err == nil {
		return nil
	}
	return &VendorError{Vendor: vendor, Op: op, Cause: err}
}

// ErrNotImplemented marks an adapter method whose vendor-side contract
// isn't nailed down yet.
func ErrNotImplemented(vendor, op string) error {
	return &VendorError{Vendor: vendor, Op: op, Code: "not_implemented"}
}
