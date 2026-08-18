// Package domain holds the vendor-agnostic types every adapter must
// produce and every handler/service consumes. Nothing in here imports a
// vendorclients/* package.
package domain

import "context"

type SourceKind string

const (
	KindRTSP  SourceKind = "rtsp"  // needs a remux job; played back via MediaMTX
	KindEmbed SourceKind = "embed" // opaque URL; frontend iframes it directly
	KindHLS   SourceKind = "hls"   // direct, CORS-open .m3u8 playlist; frontend plays it straight, no remux/iframe
)

// LiveSource is what every vendor adapter must resolve a request into.
type LiveSource struct {
	Kind SourceKind

	Remux RemuxInput // set when Kind == KindRTSP

	EmbedURL string // set when Kind == KindEmbed
	HLSURL   string // set when Kind == KindHLS
}

// RemuxInput is anything the ffmpeg supervisor can consume as an upstream.
// Exactly one of URL / Run is set. HTTP vendors (Castmaster) give a stable
// URL — the service builds the remux job itself. Socket vendors (Chemito
// N9M) give a ready-built Run instead, because their upstream connection
// must be freshly renegotiated on every supervisor restart, not just the
// first start — only the adapter knows how to redo that negotiation.
type RemuxInput struct {
	URL string
	Run func(ctx context.Context) error
}

// StreamRequest is the single request shape the service layer works with.
// VendorParams carries whatever extra identifiers a given vendor needs
// (terid, dsno, plateNo, ...) without forcing every vendor into one rigid
// field set. RTSPOut is filled in by the service (it owns the {bus}_{cam}
// naming convention) before calling the adapter, for the Run-based case
// above where the adapter needs it to build its own remux closure.
type StreamRequest struct {
	Bus, Vendor  string
	Cam          int
	Main, Audio  bool
	VendorParams map[string]string
	RTSPOut      string
}

// StreamResult is the single response shape returned to the frontend,
// regardless of which vendor served the request. Kind is explicit (not
// left for the frontend to infer from which URL field is set) so the
// player-vs-iframe decision is a switch on one field, not a presence check.
type StreamResult struct {
	Key      string     `json:"key"`
	Kind     SourceKind `json:"kind"`
	RTSPOut  string     `json:"rtspOut,omitempty"`
	EmbedURL string     `json:"embedUrl,omitempty"`
	HLSURL   string     `json:"hlsUrl,omitempty"`
}

// Camera is one selectable channel/vehicle a vendor can offer, from
// Adapter.ListCameras — used both for a frontend picker and to auto-populate
// GET /api/fleet with everything a vendor account knows about, not just
// buses someone has explicitly started or listed in config/buses.json.
type Camera struct {
	VendorID, Label string
	// VendorParams, if set, is ready to hand straight to ResolveLiveSource
	// for this camera (e.g. {"plateNo": VendorID}) — lets a caller start
	// it without knowing which param key this vendor expects.
	VendorParams map[string]string
	// Online is a best-effort liveness signal from the vendor's own
	// account-wide listing (not the same as "currently bridged" — a
	// vendor may report a device online without anyone having called
	// StartStream for it yet). False when the vendor has no such signal.
	Online bool
}
