package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// IngestClient is this API's entire relationship with the media-MTX
// service (media-MTX/cmd/media-mtxd) — and therefore with MediaMTX, ffmpeg,
// and RTSP, none of which this process knows anything else about.
//
// The split: vendors that hand back a directly playable URL (Chemito's
// HTTP-FLV, Sumith's HLS/embed) are resolved here and played by the browser
// with nothing in between. Vendors whose only delivery mechanism is a
// remux into a media server (n9m, castmaster) live entirely in media-MTX;
// this client is how we ask it what is ingesting and to start or stop a
// channel.
type IngestClient struct {
	BaseURL string // e.g. http://127.0.0.1:8090
	HTTP    *http.Client
}

// NewIngestClient returns a client for the media-MTX service at baseURL.
// The timeout is generous enough for a vendor login inside Start (which
// dials the device's server) but bounded so a wedged ingest service can't
// hang a fleet request.
func NewIngestClient(baseURL string) *IngestClient {
	return &IngestClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// IngestPath is one path currently ingesting into the media server, as
// reported by GET /paths.
type IngestPath struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Tracks        []string `json:"tracks"`
	BytesReceived uint64   `json:"bytesReceived"`
	Readers       []struct {
		Type string `json:"type"`
	} `json:"readers"`
}

// Paths reports every channel currently ingesting. Replaces this API's old
// direct call to MediaMTX's /paths/list — including its paging, which is
// now media-MTX's problem.
func (c *IngestClient) Paths(ctx context.Context) ([]IngestPath, error) {
	var out []IngestPath
	err := c.get(ctx, "/paths", &out)
	return out, err
}

// IngestCamera is one device an ingest-side vendor reports knowing about.
type IngestCamera struct {
	VendorID     string            `json:"vendorId"`
	Label        string            `json:"label"`
	Vendor       string            `json:"vendor"`
	Online       bool              `json:"online"`
	Channels     int               `json:"channels"`
	VendorParams map[string]string `json:"vendorParams"`
}

// Cameras lists what the ingest service's own vendors know about, so the
// fleet roster can include them without this process knowing those vendors
// exist.
func (c *IngestClient) Cameras(ctx context.Context) ([]IngestCamera, error) {
	var out []IngestCamera
	err := c.get(ctx, "/cameras", &out)
	return out, err
}

// IngestJob mirrors the ingest supervisor's per-job status.
type IngestJob struct {
	Key           string `json:"key"`
	Attempts      int    `json:"attempts"`
	Streaming     bool   `json:"streaming"`
	UptimeSeconds int    `json:"uptimeSeconds,omitempty"`
	LastErr       string `json:"lastError,omitempty"`
}

// Jobs reports the ingest service's active remux jobs — what GET
// /api/bridge surfaces.
func (c *IngestClient) Jobs(ctx context.Context) ([]IngestJob, error) {
	var out []IngestJob
	err := c.get(ctx, "/jobs", &out)
	return out, err
}

// IngestStartResult is what the ingest service returns once a remux job is
// running: the media-server path the stream now publishes to.
type IngestStartResult struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	RTSPOut string `json:"rtspOut"`
}

// Start asks the ingest service to bring one bus/cam up. Errors carry the
// service's own message, which already distinguishes an offline device from
// an unconfigured vendor.
func (c *IngestClient) Start(ctx context.Context, bus string, cam int, vendor string, main, audio bool, params map[string]string) (IngestStartResult, error) {
	body, err := json.Marshal(map[string]any{
		"bus": bus, "cam": cam, "vendor": vendor,
		"main": main, "audio": audio, "vendorParams": params,
	})
	if err != nil {
		return IngestStartResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/start", bytes.NewReader(body))
	if err != nil {
		return IngestStartResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return IngestStartResult{}, fmt.Errorf("ingest service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := readLimited(resp)
		return IngestStartResult{}, fmt.Errorf("ingest start %s_%d: HTTP %d: %s", bus, cam, resp.StatusCode, msg)
	}
	var out IngestStartResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return IngestStartResult{}, err
	}
	return out, nil
}

// Stop asks the ingest service to tear a job down. Reports whether it had
// one under that key.
func (c *IngestClient) Stop(ctx context.Context, key string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/stop?key="+key, nil)
	if err != nil {
		return false
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var out struct {
		Stopped bool `json:"stopped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	return out.Stopped
}

func (c *IngestClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ingest service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := readLimited(resp)
		return fmt.Errorf("ingest %s: HTTP %d: %s", path, resp.StatusCode, msg)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// readLimited returns a bounded slice of an error response body, so a
// misbehaving upstream can't put an unbounded string into our logs.
func readLimited(resp *http.Response) (string, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 2<<10))
	return buf.String(), err
}
