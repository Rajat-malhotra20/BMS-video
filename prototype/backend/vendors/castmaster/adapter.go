// Package castmaster adapts vendorclients/castmaster to the
// vendors.Adapter contract.
package castmaster

import (
	"context"

	"mediamtx-console/domain"
	rawclient "mediamtx-console/vendorclients/castmaster"
)

// Config is the per-vendor account block loaded from config/vendors.json.
type Config struct {
	BaseURL, Username, Password string
}

type Adapter struct {
	cfg Config
}

func New(cfg Config) *Adapter { return &Adapter{cfg: cfg} }

func (a *Adapter) Name() string { return "castmaster" }

// ResolveLiveSource logs in, resolves a relay port, and requests the FLV
// live-video URL for one device channel (§4.1-4.2 of the doc). Always
// KindRTSP — Castmaster hands back a raw stream URL to remux.
func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	if _, err := client.Login(a.cfg.Username, a.cfg.Password); err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("castmaster", "login", err)
	}

	ports, err := client.LivePorts()
	if err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("castmaster", "list live ports", err)
	}
	if len(ports) == 0 {
		return domain.LiveSource{}, &domain.VendorError{
			Vendor: "castmaster", Op: "resolve live source", Code: "no_live_ports", Retryable: true,
		}
	}

	terid := req.VendorParams["terid"]
	channel := req.Cam // the vendor's device channel — same number as our own {bus}_{cam}, not a separate config value
	if channel == 0 {
		channel = 1
	}

	st := rawclient.LiveStreamSub
	if req.Main {
		st = rawclient.LiveStreamMain
	}
	url, err := client.LiveVideoURL(terid, channel, req.Audio, st, ports[0].Port, rawclient.DeviceN9M)
	if err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("castmaster", "resolve live video url", err)
	}

	return domain.LiveSource{Kind: domain.KindRTSP, Remux: domain.RemuxInput{URL: url}}, nil
}

// ListCameras: the doc has no "list channels for this device" call (only
// login, alarm, status, live/history/download and evidence interfaces).
func (a *Adapter) ListCameras(ctx context.Context, params map[string]string) ([]domain.Camera, error) {
	return nil, domain.ErrNotImplemented("castmaster", "list cameras (not in the published API doc)")
}
