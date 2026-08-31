package main

import (
	"context"
	"sync/atomic"
	"testing"

	"mediamtx-console/domain"
	"mediamtx-console/services"
	"mediamtx-console/vendors"
)

// countingAdapter is a vendor that only records how often its device list
// was asked for — the call that mints the shared verify key.
type countingAdapter struct{ calls atomic.Int32 }

func (c *countingAdapter) Name() string { return "counting" }

func (c *countingAdapter) ResolveLiveSource(context.Context, domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV}, nil
}

func (c *countingAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	c.calls.Add(1)
	return []domain.Camera{{VendorID: "BUS", Label: "BUS", Channels: 9, Online: true}}, nil
}

// The boot-time warm-up in main() exists so the first viewer after a
// redeploy doesn't wait for a vendor login. That only holds if the sweep it
// calls actually primes the caches every later request reads — otherwise the
// warm-up is a wasted login and, worse than wasted, a second login racing
// the first tile would invalidate its stream token (see
// vendors/chemitoapi.Adapter.session).
func TestWarmUpPrimesTheVendorCaches(t *testing.T) {
	vendor := &countingAdapter{}
	svc := &services.StreamService{Registry: vendors.NewRegistry(vendor)}
	ctx := context.Background()

	// What main() calls at boot.
	if counts := svc.ChannelCounts(ctx); counts["BUS"] != 9 {
		t.Fatalf("channel counts = %v, want BUS: 9", counts)
	}
	afterWarmUp := vendor.calls.Load()
	if afterWarmUp != 1 {
		t.Fatalf("warm-up swept the vendor %d times, want 1", afterWarmUp)
	}

	// What the first request after boot does. Both must come from cache:
	// a vendor call here is a login the first viewer pays for.
	svc.ChannelCounts(ctx)
	svc.VendorRoster(ctx)
	if got := vendor.calls.Load(); got != afterWarmUp {
		t.Fatalf("requests after warm-up cost %d more vendor sweep(s), want 0", got-afterWarmUp)
	}
}
