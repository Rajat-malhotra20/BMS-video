// Package chemitoapi adapts vendorclients/chemitoapi (Chemito's HTTP API,
// docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx) to the vendors.Adapter
// contract. Distinct from Chemito's other integration path
// (vendorclients/n9m + n9mserver, a device-initiated TCP protocol) — this
// one is a plain HTTP vendor, same shape as vendors/sumithlive.
package chemitoapi

import (
	"context"

	"mediamtx-console/domain"
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

// ResolveLiveSource logs in, resolves the device's own relay port, and
// requests the FLV live-video URL (§4 of the doc) — always KindRTSP, since
// Chemito hands back a raw stream URL to remux, same as castmaster.
func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	if _, err := client.Login(a.cfg.Username, a.cfg.Password); err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("chemitoapi", "login", err)
	}

	terid := req.VendorParams["terid"]

	// LivePorts()/"/live/port" always returns an empty list on this
	// account (see client.go), so the port comes from the device's own
	// "transmitport" field instead — devices list is small, one round trip.
	devices, err := client.ListDevices()
	if err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("chemitoapi", "list devices", err)
	}
	var port int
	for _, d := range devices {
		if d.Terid == terid {
			port = d.TransmitPort
			break
		}
	}
	if port == 0 {
		return domain.LiveSource{}, &domain.VendorError{
			Vendor: "chemitoapi", Op: "resolve live source", Code: "no_live_ports", Retryable: true,
		}
	}

	channel := req.Cam // the vendor's device channel — same number as our own {bus}_{cam}, not a separate config value
	if channel == 0 {
		channel = 1
	}

	st := rawclient.LiveStreamSub
	if req.Main {
		st = rawclient.LiveStreamMain
	}
	url, err := client.LiveVideoURL(terid, channel, req.Audio, st, port)
	if err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("chemitoapi", "resolve live video url", err)
	}

	return domain.LiveSource{Kind: domain.KindRTSP, Remux: domain.RemuxInput{URL: url}}, nil
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
		}
	}
	return cams, nil
}
