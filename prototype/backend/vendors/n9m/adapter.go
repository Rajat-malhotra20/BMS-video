// Package n9m adapts the Chemito N9M device server (vendorclients/n9m +
// n9mserver) to the vendors.Adapter contract. Unlike the HTTP vendors,
// there's no outbound login call — the device connects to us — so
// ResolveLiveSource just looks the device up in the registry and builds
// the remux job as a domain.RemuxInput.Run closure (not a static URL),
// because each supervisor restart must re-issue RequestLiveVideo: an
// N9M device's media channel can't be reused once its stream ends.
package n9m

import (
	"context"
	"strconv"
	"time"

	"mediamtx-console/domain"
	"mediamtx-console/vendorclients/bridge"
	rawclient "mediamtx-console/vendorclients/n9m"
	"mediamtx-console/vendorclients/n9mserver"
)

type Adapter struct {
	server *n9mserver.Server
}

// New wraps the n9mserver.Server already listening for device connections
// (built once in main.go and shared with the rest of the app).
func New(server *n9mserver.Server) *Adapter { return &Adapter{server: server} }

func (a *Adapter) Name() string { return "n9m" }

func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	dsno := req.VendorParams["dsno"]
	if _, ok := a.server.Registry.Get(dsno); !ok {
		return domain.LiveSource{}, &domain.VendorError{
			Vendor: "n9m", Op: "resolve live source", Code: "device_offline", Retryable: true,
		}
	}

	channel := req.Cam // the vendor's device channel — same number as our own {bus}_{cam}, not a separate config value
	if channel == 0 {
		channel = 1
	}
	channelBit := uint32(1) << (channel - 1)

	streamType := 0 // sub
	if req.Main {
		streamType = 1 // main
	}

	format := bridge.StdinFormat(req.VendorParams["format"])
	if format == "" {
		format = bridge.StdinFormatH264
	}

	streamName := req.Bus + "_" + strconv.Itoa(req.Cam)
	ipAndPort := req.VendorParams["ipAndPort"]
	rtspOut := req.RTSPOut
	server := a.server

	run := func(ctx context.Context) error {
		mediaConn, err := server.RequestLiveVideo(dsno, streamName, rawclient.RequestAliveVideoParams{
			StreamType: streamType,
			Channel:    channelBit,
			IPAndPort:  ipAndPort,
		}, 15*time.Second)
		if err != nil {
			return err
		}
		defer mediaConn.Close()
		reader := n9mserver.NewMediaFrameReader(mediaConn, rawclient.PayloadLiveVideo)
		return bridge.StreamStdinToRTSP(reader, format, rtspOut)(ctx)
	}

	return domain.LiveSource{Kind: domain.KindRTSP, Remux: domain.RemuxInput{Run: run}}, nil
}

// ListCameras reports currently-connected devices (there's no separate
// per-device channel list in this protocol — channel is just a bitmask
// picked at request time).
func (a *Adapter) ListCameras(ctx context.Context, params map[string]string) ([]domain.Camera, error) {
	sessions := a.server.Registry.List()
	cams := make([]domain.Camera, len(sessions))
	for i, d := range sessions {
		cams[i] = domain.Camera{
			VendorID:     d.DSNO,
			Label:        d.Info.CarNum,
			Online:       true, // being in this list means the device has an open connection right now
			VendorParams: map[string]string{"dsno": d.DSNO},
		}
	}
	return cams, nil
}
