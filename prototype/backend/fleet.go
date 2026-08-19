package main

import (
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// mtxPath is the subset of MediaMTX /v3/paths/list items we need.
type mtxPath struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Tracks        []string `json:"tracks"`
	BytesReceived uint64   `json:"bytesReceived"`
	Readers       []struct {
		Type string `json:"type"`
	} `json:"readers"`

	// DirectKind/DirectURL are set on a synthetic entry from
	// apiServer.directPaths — a bus live via a non-RTSP vendor result
	// ("embed" or "hls"), no MediaMTX ingest at all. Empty for a real
	// MediaMTX path; never set by MediaMTX itself, JSON-ignored since
	// neither ever appears in the real /paths/list response.
	DirectKind string `json:"-"`
	DirectURL  string `json:"-"`
}

type fleetBus struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"` // human-readable name (plate, car licence, ...) from a vendor roster — omitted when the id is already the readable name
	Cams     []int  `json:"cams"`            // camera numbers currently live (actually streaming right now)
	LastSeen int64  `json:"lastSeen"`        // unix seconds any cam was last live
	// CamsAvailable is how many camera slots the vendor reports this bus
	// having, independent of whether any are currently streaming — a cheap,
	// no-session device-listing signal (see StreamService.ChannelCounts),
	// refreshed on a slow cadence so a dashboard can show "this bus has N
	// cameras" without anyone having opened one yet. 0 when unknown.
	CamsAvailable int `json:"camsAvailable,omitempty"`
}

type fleetTotals struct {
	BusesOnline int `json:"busesOnline"`
	BusesSeen   int `json:"busesSeen"`
	CamsOnline  int `json:"camsOnline"`
}

type fleetSummary struct {
	Buses     []fleetBus  `json:"buses"`
	Totals    fleetTotals `json:"totals"`
	UpdatedAt int64       `json:"updatedAt"`
}

var busPathRe = regexp.MustCompile(`^(?:.*/)?([A-Za-z0-9]+)_([1-9])$`)

// parseBusPath extracts bus id and camera number from a stream path name.
func parseBusPath(name string) (busID string, cam int, ok bool) {
	m := busPathRe.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	cam, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], cam, true
}

const recentlySeenWindow = 10 * time.Minute

// fleetTracker remembers when each bus was last seen so recently-offline
// buses stay visible (red) in the list for recentlySeenWindow.
type fleetTracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newFleetTracker() *fleetTracker {
	return &fleetTracker{lastSeen: make(map[string]time.Time)}
}

func (t *fleetTracker) build(paths []mtxPath, now time.Time) fleetSummary {
	t.mu.Lock()
	defer t.mu.Unlock()

	online := make(map[string][]int)
	for _, p := range paths {
		if !p.Ready {
			continue
		}
		busID, cam, ok := parseBusPath(p.Name)
		if !ok {
			continue
		}
		online[busID] = append(online[busID], cam)
		t.lastSeen[busID] = now
	}

	var summary fleetSummary
	camsOnline := 0
	for busID, seen := range t.lastSeen {
		if now.Sub(seen) > recentlySeenWindow {
			delete(t.lastSeen, busID)
			continue
		}
		cams := online[busID]
		if cams == nil {
			cams = []int{}
		}
		sort.Ints(cams)
		camsOnline += len(cams)
		summary.Buses = append(summary.Buses, fleetBus{
			ID:       busID,
			Cams:     cams,
			LastSeen: seen.Unix(),
		})
	}

	sort.Slice(summary.Buses, func(i, j int) bool {
		a, errA := strconv.Atoi(summary.Buses[i].ID)
		b, errB := strconv.Atoi(summary.Buses[j].ID)
		if errA == nil && errB == nil {
			return a < b
		}
		return summary.Buses[i].ID < summary.Buses[j].ID
	})

	summary.Totals = fleetTotals{
		BusesOnline: len(online),
		BusesSeen:   len(summary.Buses),
		CamsOnline:  camsOnline,
	}
	summary.UpdatedAt = now.Unix()
	if summary.Buses == nil {
		summary.Buses = []fleetBus{}
	}
	return summary
}
