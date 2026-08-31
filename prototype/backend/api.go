package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"mediamtx-console/domain"
	"mediamtx-console/services"
)

type apiServer struct {
	// ingest is this API's only link to the media-MTX service, and
	// therefore the only way it learns what is ingesting. There is no
	// MediaMTX address, client, or paging logic in this process any more —
	// see services.IngestClient.
	ingest  *services.IngestClient
	tracker *fleetTracker

	// directKeys, if set, returns {bus}_{cam} keys currently live via a
	// non-RTSP vendor result (embed or direct HLS) — never in MediaMTX, so
	// they'd otherwise be invisible to GET /api/fleet. Wired in main.go
	// once StreamService exists (apiServer is constructed first).
	directKeys func() []services.DirectEntry

	// vendorRoster, if set, sweeps every vendor account's own listing
	// (e.g. Sumith's full vehicle list) so GET /api/fleet shows everything
	// a vendor reports knowing about, not just buses someone has
	// explicitly bridged or listed in config/buses.json.
	vendorRoster func(ctx context.Context) []services.RosterEntry

	// ensureStream, if set, is called by handleStreamLive when a
	// specifically-requested ?cam= isn't already active: it starts the
	// bridge on demand and returns the result, so the frontend never has
	// to call POST /api/bridge/start itself. Returns (nil, nil) when the
	// bus isn't configured for bridging at all.
	ensureStream func(ctx context.Context, bus string, cam int) (*domain.StreamResult, error)

	// channelCounts, if set, reports how many camera slots a vendor says
	// each bus has, independent of whether any are currently streaming —
	// display-only, no live session involved. Slow-refreshing (see
	// StreamService.ChannelCounts) since it rarely changes.
	channelCounts func(ctx context.Context) map[string]int

	// unwired, if set, reports a {bus}_{cam} the device has already proven
	// has no camera on it (see flvHub.Unwired). The vendor's channel count
	// is a slot count, not a camera count, and offering the empty slots
	// costs real sessions on a device that has few.
	unwired func(key string) bool

	// flvLive, if set, reports whether an FLV key has an upstream that has
	// actually produced video, and whether the hub knows the key at all
	// (see flvHub.Live). The direct-entry registry cannot answer this: it
	// records what was started, not what is streaming.
	flvLive func(key string) (live, known bool)

	cacheMu     sync.Mutex
	cachedFleet *fleetSummary
	cachedPaths []ingestPath
	cachedAt    time.Time
	refreshing  bool
	refreshDone chan struct{}
	lastErr     error
}

const fleetCacheTTL = 2 * time.Second

// newAPIServer wires the API to the media-MTX service at ingestBase
// (e.g. http://127.0.0.1:8090). An empty ingestBase means there is no
// ingest service at all: a deployment that runs neither MediaMTX nor
// media-mtxd, serving only vendors that need no media server (Chemito
// HTTP-FLV, Sumith HLS/embed). That is a supported configuration, not a
// broken one, so nothing here treats a missing ingest side as fatal.
func newAPIServer(ingestBase string) *apiServer {
	a := &apiServer{tracker: newFleetTracker()}
	if ingestBase != "" {
		a.ingest = services.NewIngestClient(ingestBase)
	}
	return a
}

// directPaths turns currently-active non-RTSP vendor sessions into
// synthetic ingestPath entries (no tracks/bytes) so fleetTracker.build can
// fold them into the fleet summary using the same {bus}_{cam} parsing it
// already applies to real MediaMTX paths. DirectKind carries which kind
// (embed/hls/flv) so camDetail/streamInfo can report it accurately instead
// of assuming "embed" for anything synthetic.
//
// Ready is the part that needs care. A direct entry is registered when a
// stream is started and dropped only by an explicit stop, so its presence
// means "someone started this once", not "video is flowing". For FLV the
// hub holds the upstream and therefore knows the difference — so ask it,
// and a channel it has never got a frame from (an unwired channel number, a
// device that stopped answering) stops being counted as a live camera.
// Without this GET /api/fleet reported cams 8 and 9 of DLPD8611 as online
// with nothing behind them, and disagreed with GET /api/hub.
func (a *apiServer) directPaths() []ingestPath {
	if a.directKeys == nil {
		return nil
	}
	entries := a.directKeys()
	paths := make([]ingestPath, len(entries))
	for i, e := range entries {
		url := e.EmbedURL
		if url == "" {
			url = e.HLSURL
		}
		ready := true
		if e.Kind == domain.KindFLV && a.flvLive != nil {
			// known=false means no channel at all: nobody is watching, so
			// there is no session and nothing to call live.
			live, known := a.flvLive(e.Key)
			ready = known && live
		}
		paths[i] = ingestPath{Name: e.Key, Ready: ready, DirectKind: string(e.Kind), DirectURL: url}
	}
	return paths
}

// vendorRosterEntries is a.vendorRoster with a nil-safe check, so callers
// don't each need to guard it separately.
func (a *apiServer) vendorRosterEntries(ctx context.Context) []services.RosterEntry {
	if a.vendorRoster == nil {
		return nil
	}
	return a.vendorRoster(ctx)
}

// mergePaths de-dupes by Name, keeping the highest-priority source: real
// MediaMTX ingest first, then an explicitly-started embed session — an
// active session or real stream is always more authoritative than "the
// vendor account reports knowing about this vehicle" (that's handled by
// mergeRoster below, on the built summary, not here).
func mergePaths(sources ...[]ingestPath) []ingestPath {
	seen := make(map[string]bool)
	var merged []ingestPath
	for _, src := range sources {
		for _, p := range src {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			merged = append(merged, p)
		}
	}
	return merged
}

// mergeRoster adds every vendor-roster bus tracker.build didn't already
// include, so a bus the vendor reports knowing about always shows up in
// GET /api/fleet — even one that's never actually streamed and so would
// otherwise never enter fleetTracker's seen-recently bookkeeping. Unlike
// tracker.build's MediaMTX-sourced buses, these don't linger past their
// vendor-reported state: absent from this call's roster means gone right
// now, not a 10-minute grace window.
//
// Cams is always left empty here, deliberately: a roster entry's Online
// signal (e.g. Sumith's GPS fix) confirms the *vehicle* is live, not that
// a camera exists or works on it — confirmed live-tested that Sumith's
// getLiveStreamingLink returns "success" for literally any channel_id
// (0, 1, 4, 99, all identical), so it validates nothing about real
// channel count. Only an actual started session (tracker.build's real
// MediaMTX/embed-active signal, already merged into summary before this
// runs) earns a slot in Cams/CamsOnline. BusesOnline still reflects the
// roster signal, since "is this vehicle around" is a real answer even
// when "does it have a working camera" isn't.
func mergeRoster(summary fleetSummary, roster []services.RosterEntry) fleetSummary {
	have := make(map[string]bool, len(summary.Buses))
	for _, b := range summary.Buses {
		have[b.ID] = true
	}
	for _, e := range roster {
		busID, _, ok := parseBusPath(e.Key)
		if !ok || have[busID] {
			continue
		}
		have[busID] = true

		if e.Online {
			summary.Totals.BusesOnline++
		}
		summary.Totals.BusesSeen++
		summary.Buses = append(summary.Buses, fleetBus{ID: busID, Label: e.Label, Cams: []int{}, LastSeen: time.Now().Unix()})
	}
	sort.Slice(summary.Buses, func(i, j int) bool {
		a, errA := strconv.Atoi(summary.Buses[i].ID)
		b, errB := strconv.Atoi(summary.Buses[j].ID)
		if errA == nil && errB == nil {
			return a < b
		}
		return summary.Buses[i].ID < summary.Buses[j].ID
	})
	return summary
}

// snapshot returns cached paths+summary, refreshing from MediaMTX when stale.
// Issue 1: stampede-safe — lock is NOT held across HTTP fetches.
func (a *apiServer) snapshot() (*fleetSummary, []ingestPath, error) {
	a.cacheMu.Lock()

	// Cache is fresh — return immediately.
	if a.cachedFleet != nil && time.Since(a.cachedAt) < fleetCacheTTL {
		fleet, paths := a.cachedFleet, a.cachedPaths
		a.cacheMu.Unlock()
		return fleet, paths, nil
	}

	// Another goroutine is already refreshing — wait for it.
	if a.refreshing {
		done := a.refreshDone
		a.cacheMu.Unlock()
		<-done
		a.cacheMu.Lock()
		fleet, paths, err := a.cachedFleet, a.cachedPaths, a.lastErr
		a.cacheMu.Unlock()
		if fleet == nil && err == nil {
			err = fmt.Errorf("cache unavailable after refresh")
		}
		return fleet, paths, err
	}

	// We are the designated refresher.
	a.refreshing = true
	a.refreshDone = make(chan struct{})
	done := a.refreshDone
	a.cacheMu.Unlock()

	// Fetch and build WITHOUT holding the lock.
	// The ingest service is one OPTIONAL source of paths, deliberately not
	// a hard dependency. Vendors that hand back a directly playable URL
	// never touch it, so a deployment can legitimately run without MediaMTX
	// and media-mtxd entirely — and even where they do run, one of them
	// being down must not blank a fleet whose buses are all Chemito/Sumith.
	// A failure here is logged and treated as "no RTSP-ingested channels",
	// which is exactly what it means.
	var paths []ingestPath
	if a.ingest != nil {
		ingestPaths, err := a.ingest.Paths(context.Background())
		if err != nil {
			log.Printf("fleet: ingest service unavailable, continuing without RTSP paths: %v", err)
		}
		paths = make([]ingestPath, len(ingestPaths))
		for i, p := range ingestPaths {
			paths[i] = ingestPath{
				Name: p.Name, Ready: p.Ready, Tracks: p.Tracks,
				BytesReceived: p.BytesReceived, Readers: p.Readers,
			}
		}
	}
	paths = mergePaths(paths, a.directPaths())
	summary := a.tracker.build(paths, time.Now())
	summary = mergeRoster(summary, a.vendorRosterEntries(context.Background()))

	// Write results back under the lock. There is no failure path left to
	// record: a fleet built from direct sessions and the vendor roster is a
	// complete answer even with nothing ingesting. The error return is kept
	// so callers keep their defensive handling if a future source of paths
	// is genuinely required.
	a.cacheMu.Lock()
	a.cachedFleet = &summary
	a.cachedPaths = paths
	a.cachedAt = time.Now()
	a.lastErr = nil
	a.refreshing = false
	a.cacheMu.Unlock()

	// Wake all waiters.
	close(done)

	return a.cachedFleet, a.cachedPaths, nil
}

// unwiredCams counts how many of bus's first n channel numbers have been
// proven to have no camera on them.
//
// This is what makes camsAvailable answerable by a frontend. The vendor
// reports a slot count, not a camera count — 9 on these DVRs, of which 7
// are real — so a dashboard rendering camsAvailable directly promises two
// cameras that do not exist, and the only other signal it has (cams) is
// empty whenever nobody happens to be watching. Subtracting what the device
// itself has refused means "does this bus have cameras, and how many" can be
// answered without opening a single vendor session.
//
// The memory expires (flvUnwiredMemory), so a count can drift back up until
// the empty channel is proven again. That is the right direction to be
// wrong in: a camera newly wired to channel 8 appears on its own.
func (a *apiServer) unwiredCams(bus string, n int) int {
	if a.unwired == nil || n <= 0 {
		return 0
	}
	dead := 0
	for cam := 1; cam <= n; cam++ {
		if a.unwired(bus + "_" + strconv.Itoa(cam)) {
			dead++
		}
	}
	return dead
}

// fleetSummaryWithCounts is handleFleet's and handleFleetStream's shared
// core: the cached fleet snapshot, annotated with each bus's camsAvailable
// count.
func (a *apiServer) fleetSummaryWithCounts(ctx context.Context) (fleetSummary, error) {
	summary, _, err := a.snapshot()
	if err != nil {
		return fleetSummary{}, err
	}

	out := *summary
	if a.channelCounts != nil {
		// Copy before mutating — summary is the shared cached pointer;
		// annotating it in place would race with concurrent readers and
		// leak into the cache.
		counts := a.channelCounts(ctx)
		buses := make([]fleetBus, len(out.Buses))
		copy(buses, out.Buses)
		for i := range buses {
			buses[i].CamsAvailable = counts[buses[i].ID] - a.unwiredCams(buses[i].ID, counts[buses[i].ID])
		}
		out.Buses = buses
	}
	return out, nil
}

func (a *apiServer) handleFleet(w http.ResponseWriter, r *http.Request) {
	summary, err := a.fleetSummaryWithCounts(r.Context())
	if err != nil {
		log.Printf("fleet: mediamtx api error: %v", err)
		http.Error(w, "mediamtx unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, summary)
}

// handleFleetStream serves the same data as GET /api/fleet, pushed over
// Server-Sent Events as it changes instead of polled — so a frontend gets
// updates as they happen without busy-polling /api/fleet. Plain net/http +
// http.Flusher; no WebSocket library needed since this is one-way
// (server → client) and the client never needs to send anything back over
// this connection. A frame is only sent when the summary actually changed
// since the last one, so an idle fleet doesn't spam empty updates.
func (a *apiServer) handleFleetStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastSent string
	for {
		if summary, err := a.fleetSummaryWithCounts(r.Context()); err == nil {
			if body, err := json.Marshal(summary); err == nil && string(body) != lastSent {
				fmt.Fprintf(w, "data: %s\n\n", body)
				flusher.Flush()
				lastSent = string(body)
			}
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

type camDetail struct {
	Cam           int      `json:"cam"`
	Path          string   `json:"path"`
	Ready         bool     `json:"ready"`
	Tracks        []string `json:"tracks"`
	BytesReceived uint64   `json:"bytesReceived"`
	Readers       int      `json:"readers"`
	Kind          string   `json:"kind,omitempty"`      // "embed", "hls" or "flv" for a non-RTSP vendor bus; omitted for real MediaMTX ingest
	DirectURL     string   `json:"directUrl,omitempty"` // the embed page or direct .m3u8 URL when Kind is set
}

type busDetail struct {
	ID   string      `json:"id"`
	Cams []camDetail `json:"cams"`
}

func (a *apiServer) handleBusDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, paths, err := a.snapshot()
	if err != nil {
		log.Printf("bus detail: mediamtx api error: %v", err)
		http.Error(w, "mediamtx unavailable", http.StatusBadGateway)
		return
	}
	detail := busDetail{ID: id, Cams: []camDetail{}}
	// camsForBus filters and sorts by cam number. This used to walk `paths`
	// raw, which returned cams in whatever order the ingest/direct sources
	// happened to be merged in (e.g. 2,3,1,4) while GET /api/stream/{id}
	// returned them sorted — same buses, two different orders.
	for _, p := range camsForBus(paths, id) {
		_, cam, _ := parseBusPath(p.Name)
		// Issue 4: normalize nil tracks to []string{} so JSON encodes [] not null.
		tracks := p.Tracks
		if tracks == nil {
			tracks = []string{}
		}
		cd := camDetail{
			Cam:           cam,
			Path:          p.Name,
			Ready:         p.Ready,
			Tracks:        tracks,
			BytesReceived: p.BytesReceived,
			Readers:       len(p.Readers),
		}
		if p.DirectKind != "" {
			cd.Kind = p.DirectKind
			cd.DirectURL = p.DirectURL
		}
		detail.Cams = append(detail.Cams, cd)
	}
	writeJSON(w, detail)
}

// camsForBus filters paths to those belonging to busId, sorted by cam number.
func camsForBus(paths []ingestPath, busID string) []ingestPath {
	var out []ingestPath
	for _, p := range paths {
		id, _, ok := parseBusPath(p.Name)
		if !ok || id != busID {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		_, ci, _ := parseBusPath(out[i].Name)
		_, cj, _ := parseBusPath(out[j].Name)
		return ci < cj
	})
	return out
}

type streamInfo struct {
	Cam       int    `json:"cam"`
	Path      string `json:"path"`
	Ready     bool   `json:"ready"`
	Kind      string `json:"kind,omitempty"`      // "embed", "hls" or "flv" — no MediaMTX ingest; DirectURL below carries the playable link
	WhepURL   string `json:"whepUrl,omitempty"`   // our MediaMTX proxy path — only set for real ingest (Kind empty)
	HLSURL    string `json:"hlsUrl,omitempty"`    // our MediaMTX proxy path — only set for real ingest (Kind empty)
	DirectURL string `json:"directUrl,omitempty"` // the vendor's embed page or direct .m3u8 URL — only set when Kind is set
}

func (a *apiServer) handleStreamLive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	camFilter := r.URL.Query().Get("cam")

	_, paths, err := a.snapshot()
	if err != nil {
		log.Printf("stream live: mediamtx api error: %v", err)
		http.Error(w, "mediamtx unavailable", http.StatusBadGateway)
		return
	}

	result := []streamInfo{}
	for _, p := range camsForBus(paths, id) {
		_, cam, _ := parseBusPath(p.Name)
		if camFilter != "" {
			wantCam, err := strconv.Atoi(camFilter)
			if err != nil || cam != wantCam {
				continue
			}
		}
		si := streamInfo{Cam: cam, Path: p.Name, Ready: p.Ready}
		if p.DirectKind != "" {
			si.Kind = p.DirectKind
			si.DirectURL = p.DirectURL
		} else {
			si.WhepURL = "/whep/" + p.Name + "/whep"
			si.HLSURL = "/live/" + p.Name + "/index.m3u8"
		}
		result = append(result, si)
	}

	// No specific cam asked for: hitting a bus id should hand back every
	// channel at once, which means starting the ones nobody has opened
	// yet. Without this the bare bus URL only ever reported channels some
	// earlier request happened to start — an empty array on a fresh
	// process, with nothing triggering a start.
	if camFilter == "" {
		result = a.ensureAllCams(r.Context(), id, result)
	}

	// Nothing found for the specific cam asked for — either it's already
	// active but started too recently for the (2s) fleet cache to show it
	// yet, or it's genuinely not started. Check directKeys fresh (it's an
	// in-memory read, not another vendor round trip) before deciding to
	// trigger a start — otherwise a session started a moment ago would
	// look like a no-op to ensureStream (correctly, it IS already active)
	// but never get reported back here.
	if len(result) == 0 && camFilter != "" {
		if wantCam, err := strconv.Atoi(camFilter); err == nil {
			key := id + "_" + strconv.Itoa(wantCam)

			if a.directKeys != nil {
				for _, e := range a.directKeys() {
					if e.Key == key {
						result = append(result, directEntryToInfo(e, wantCam))
						break
					}
				}
			}

			if len(result) == 0 && a.ensureStream != nil {
				started, err := a.ensureStream(r.Context(), id, wantCam)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				if started != nil {
					result = append(result, streamResultToInfo(*started, wantCam))
				}
			}
		}
	}

	writeJSON(w, result)
}

// camsPerBusFallback is how many channels a bare GET /api/stream/{id}
// brings up when the vendor reports no channel count of its own (N9M and
// Castmaster don't). A guess, but a bounded one.
const camsPerBusFallback = 4

// camsPerBusCeiling bounds the fan-out even when a vendor claims more.
// Chemito's live-video ports hold 4 channels each and the account exposes
// 4 of them, so 16 concurrent channels is the account-wide ceiling
// (PMIDTC_CIPLAPIS.xlsx §4: "4-way video on one port and 16-way video at
// most at the same time"). Asking for more than that cannot be served no
// matter which bus asks.
const camsPerBusCeiling = 16

// ensureAllCams brings up every channel of bus that isn't live yet and
// appends it to have. A channel that fails to start is logged and skipped,
// never fatal: one dead camera must not blank the other three.
func (a *apiServer) ensureAllCams(ctx context.Context, bus string, have []streamInfo) []streamInfo {
	if a.ensureStream == nil {
		return have
	}

	// The vendor's own channel count, not a fixed guess: Chemito reports 9
	// for these DVRs and 7 of them really do stream (confirmed live
	// 2026-08-26 on DL1PD8584 — 1-7 deliver, 8-9 are unwired and answer
	// 408/EOF). Capping at 4 silently hid three working cameras.
	n := camsPerBusFallback
	if a.channelCounts != nil {
		if c := a.channelCounts(ctx)[bus]; c > 0 {
			n = c
		}
	}
	if n > camsPerBusCeiling {
		n = camsPerBusCeiling
	}

	seen := make(map[int]bool, len(have))
	for _, si := range have {
		seen[si.Cam] = true
	}

	for cam := 1; cam <= n; cam++ {
		if seen[cam] {
			continue
		}
		key := bus + "_" + strconv.Itoa(cam)

		// A channel the device has already EOF'd on is not a camera. It
		// would fail the same way every time, and each attempt spends one
		// of the device's few concurrent sessions — the ones the working
		// cameras need.
		if a.unwired != nil && a.unwired(key) {
			continue
		}

		// Check the live session map before starting: the fleet snapshot
		// this result was built from is up to 2s stale, so a channel
		// started moments ago is active but missing from `have`.
		if a.directKeys != nil {
			found := false
			for _, e := range a.directKeys() {
				if e.Key == key {
					have = append(have, directEntryToInfo(e, cam))
					found = true
					break
				}
			}
			if found {
				continue
			}
		}

		started, err := a.ensureStream(ctx, bus, cam)
		if err != nil {
			log.Printf("stream live: %s: %v", key, err)
			continue
		}
		if started != nil {
			have = append(have, streamResultToInfo(*started, cam))
		}
	}

	sort.Slice(have, func(i, j int) bool { return have[i].Cam < have[j].Cam })
	return have
}

// streamResultToInfo renders a just-started domain.StreamResult in the
// same shape handleStreamLive already uses for a cache-discovered entry.
func streamResultToInfo(r domain.StreamResult, cam int) streamInfo {
	si := streamInfo{Cam: cam, Path: r.Key, Ready: true}
	switch r.Kind {
	case domain.KindEmbed:
		si.Kind = "embed"
		si.DirectURL = r.EmbedURL
	case domain.KindHLS, domain.KindFLV:
		si.Kind = string(r.Kind)
		si.DirectURL = r.HLSURL
	default:
		si.WhepURL = "/whep/" + r.Key + "/whep"
		si.HLSURL = "/live/" + r.Key + "/index.m3u8"
	}
	return si
}

// directEntryToInfo renders an already-active services.DirectEntry (found
// via a.directKeys, bypassing a possibly-stale fleet cache) the same way.
func directEntryToInfo(e services.DirectEntry, cam int) streamInfo {
	si := streamInfo{Cam: cam, Path: e.Key, Ready: true, Kind: string(e.Kind)}
	if e.Kind == domain.KindEmbed {
		si.DirectURL = e.EmbedURL
	} else {
		si.DirectURL = e.HLSURL
	}
	return si
}

type recordingInfo struct {
	Cam  int    `json:"cam"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

func (a *apiServer) handleStreamRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	camFilter := r.URL.Query().Get("cam")

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		http.Error(w, "from and to query params are required", http.StatusBadRequest)
		return
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		http.Error(w, "invalid from: "+err.Error(), http.StatusBadRequest)
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		http.Error(w, "invalid to: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}
	duration := to.Sub(from).Seconds()

	_, paths, err := a.snapshot()
	if err != nil {
		log.Printf("stream recording: mediamtx api error: %v", err)
		http.Error(w, "mediamtx unavailable", http.StatusBadGateway)
		return
	}

	result := []recordingInfo{}
	for _, p := range camsForBus(paths, id) {
		_, cam, _ := parseBusPath(p.Name)
		if camFilter != "" {
			wantCam, err := strconv.Atoi(camFilter)
			if err != nil || cam != wantCam {
				continue
			}
		}
		q := url.Values{}
		q.Set("path", p.Name)
		q.Set("start", from.Format(time.RFC3339))
		q.Set("duration", strconv.FormatFloat(duration, 'f', -1, 64))
		q.Set("format", "mp4")
		result = append(result, recordingInfo{
			Cam:  cam,
			Path: p.Name,
			URL:  "/playback/get?" + q.Encode(),
		})
	}
	writeJSON(w, result)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
