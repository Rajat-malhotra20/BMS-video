// Package chemitoapi adapts vendorclients/chemitoapi (Chemito's HTTP API,
// docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx) to the vendors.Adapter
// contract. Distinct from Chemito's other integration path
// (vendorclients/n9m + n9mserver, a device-initiated TCP protocol) — this
// one is a plain HTTP vendor, same shape as vendors/sumithlive.
package chemitoapi

import (
	"context"

	"mediamtx-console/domain"
	"mediamtx-console/vendorclients/bridge"
	rawclient "mediamtx-console/vendorclients/chemitoapi"
)

// Config is the per-vendor block loaded from config/vendors.go.
type Config struct {
	BaseURL, Username, Password string
}

type Adapter struct {
	cfg Config
}

func New(cfg Config) *Adapter { return &Adapter{cfg: cfg} }

func (a *Adapter) Name() string { return "chemitoapi" }

// ResolveLiveSource hands back a Run that logs in, resolves an available
// relay port (§4 of the doc's operation steps — "Get video port
// information" then "Get device list"), and requests the FLV live-video URL
// fresh on every Supervisor attempt — always KindRTSP, since Chemito hands
// back a raw stream URL to remux, same as castmaster.
//
// This must re-resolve every retry, not just the first: confirmed live
// 2026-08-19 that Chemito's login token / live-video URL is single-use or
// short-lived — freezing one URL into the remux job (as this used to) means
// every Supervisor retry re-execs ffmpeg against the exact same now-stale
// request, failing identically forever instead of getting a fresh chance.
// Same reasoning as N9M's Run (see domain.RemuxInput doc).
//
// The device's own "transmitport" field (from ListDevices) is NOT a
// connectable stream port — confirmed live 2026-08-19: connecting to it
// resets immediately, while a port from LivePorts() (e.g. 12060) serves the
// actual FLV stream. transmitport only reappears as the response's fixed
// "svrport" value, unrelated to the host:port you connect to.
func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	terid := req.VendorParams["terid"]
	channel := req.Cam // the vendor's device channel — same number as our own {bus}_{cam}, not a separate config value
	if channel == 0 {
		channel = 1
	}
	st := rawclient.LiveStreamSub
	if req.Main {
		st = rawclient.LiveStreamMain
	}
	rtspOut := req.RTSPOut

	run := func(ctx context.Context) error {
		client := rawclient.NewClient(a.cfg.BaseURL, nil)
		if _, err := client.Login(a.cfg.Username, a.cfg.Password); err != nil {
			return domain.WrapVendorErr("chemitoapi", "login", err)
		}

		ports, err := client.LivePorts()
		if err != nil {
			return domain.WrapVendorErr("chemitoapi", "list live ports", err)
		}
		if len(ports) == 0 {
			return &domain.VendorError{
				Vendor: "chemitoapi", Op: "resolve live source", Code: "no_live_ports", Retryable: true,
			}
		}

		url, err := client.LiveVideoURL(terid, channel, req.Audio, st, ports[0].Port)
		if err != nil {
			return domain.WrapVendorErr("chemitoapi", "resolve live video url", err)
		}

		return bridge.RemuxToRTSP(url, rtspOut)(ctx)
	}

	return domain.LiveSource{Kind: domain.KindRTSP, Remux: domain.RemuxInput{Run: run}}, nil
}

// ListCameras returns every device registered on this account, via the
// undocumented-but-confirmed-real ListDevices call (see
// vendorclients/chemitoapi.ListDevices). Online is left false: device
// registration doesn't mean the device is currently connected — that's
// only knowable via LivePorts, checked at actual start time, not here
// (calling it once per device on every roster sweep would be one more
// vendor round trip than this account-wide listing needs).
func (a *Adapter) ListCameras(ctx context.Context, params map[string]string) ([]domain.Camera, error) {
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	if _, err := client.Login(a.cfg.Username, a.cfg.Password); err != nil {
		return nil, domain.WrapVendorErr("chemitoapi", "login", err)
	}
	devices, err := client.ListDevices()
	if err != nil {
		return nil, domain.WrapVendorErr("chemitoapi", "list devices", err)
	}

	cams := make([]domain.Camera, len(devices))
	for i, d := range devices {
		// VendorID is the bus identity the frontend sees (fleet id, key
		// into config/buses.json) — that must be the plate, same as
		// sumithlive, not the vendor's internal terid. terid only lives in
		// VendorParams, for ResolveLiveSource to actually call the vendor.
		id := d.CarLicence
		if id == "" {
			id = d.Terid // no plate on file for this device — fall back rather than drop it
		}
		cams[i] = domain.Camera{
			VendorID:     id,
			Label:        id,
			VendorParams: map[string]string{"terid": d.Terid},
			Channels:     d.ChannelCount,
		}
	}
	return cams, nil
}
