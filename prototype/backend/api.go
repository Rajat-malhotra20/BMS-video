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
	mtxAPIBase string // e.g. http://mediamtx:9997/v3
	tracker    *fleetTracker
	client     *http.Client

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

	// autoStart, if set, is triggered on every GET /api/fleet hit — it
	// discovers and starts every bus/cam a vendor reports, so the fleet
	// view fills in on its own from real traffic rather than an
	// independent background poll running (and hitting vendors) even when
	// nobody's watching. Fire-and-forget: never blocks the response, and
	// throttles itself internally so rapid polling doesn't re-trigger a
	// fresh vendor sweep on every single request.
	autoStart func()

	cacheMu     sync.Mutex
	cachedFleet *fleetSummary
	cachedPaths []mtxPath
	cachedAt    time.Time
	refreshing  bool
	refreshDone chan struct{}
	lastErr     error
}

const fleetCacheTTL = 2 * time.Second

func newAPIServer(mtxAPIBase string) *apiServer {
	return &apiServer{
		mtxAPIBase: mtxAPIBase,
		tracker:    newFleetTracker(),
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

type mtxPathList struct {
	PageCount int       `json:"pageCount"`
	Items     []mtxPath `json:"items"`
}

// fetchAllPaths pages through MediaMTX /paths/list.
func (a *apiServer) fetchAllPaths() ([]mtxPath, error) {
	var all []mtxPath
	for page := 0; ; page++ {
		url := fmt.Sprintf("%s/paths/list?itemsPerPage=500&page=%d", a.mtxAPIBase, page)
		resp, err := a.client.Get(url)
		if err != nil {
			return nil, err
		}
		// Issue 2: check HTTP status before decoding.
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("mediamtx returned HTTP %d for %s", resp.StatusCode, url)
		}
		var list mtxPathList
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, list.Items...)
		// Issue 3: guard against pageCount == 0 to avoid integer underflow.
		if list.PageCount == 0 || page >= list.PageCount-1 {
			break
		}
	}
	return all, nil
}

// directPaths turns currently-active non-RTSP vendor sessions into
// synthetic mtxPath entries (Ready=true, no tracks/bytes) so
// fleetTracker.build can fold them into the fleet summary using the same
// {bus}_{cam} parsing it already applies to real MediaMTX paths.
// DirectKind carries which kind (embed/hls) so camDetail/streamInfo can
// report it accurately instead of assuming "embed" for anything synthetic.
func (a *apiServer) directPaths() []mtxPath {
	if a.directKeys == nil {
		return nil
	}
	entries := a.directKeys()
	paths := make([]mtxPath, len(entries))
	for i, e := range entries {
		url := e.EmbedURL
		if url == "" {
			url = e.HLSURL
		}
		paths[i] = mtxPath{Name: e.Key, Ready: true, DirectKind: string(e.Kind), DirectURL: url}
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
func mergePaths(sources ...[]mtxPath) []mtxPath {
	seen := make(map[string]bool)
	var merged []mtxPath
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
func (a *apiServer) snapshot() (*fleetSummary, []mtxPath, error) {
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
	paths, fetchErr := a.fetchAllPaths()
	var summary fleetSummary
	if fetchErr == nil {
		paths = mergePaths(paths, a.directPaths())
		summary = a.tracker.build(paths, time.Now())
		summary = mergeRoster(summary, a.vendorRosterEntries(context.Background()))
	}

	// Write results back under the lock.
	a.cacheMu.Lock()
	if fetchErr == nil {
		a.cachedFleet = &summary
		a.cachedPaths = paths
		a.cachedAt = time.Now()
		a.lastErr = nil
	} else {
		a.lastErr = fetchErr
	}
	a.refreshing = false
	a.cacheMu.Unlock()

	// Wake all waiters.
	close(done)

	if fetchErr != nil {
		return nil, nil, fetchErr
	}
	return a.cachedFleet, a.cachedPaths, nil
}

func (a *apiServer) handleFleet(w http.ResponseWriter, _ *http.Request) {
	if a.autoStart != nil {
		a.autoStart()
	}
	summary, _, err := a.snapshot()
	if err != nil {
		log.Printf("fleet: mediamtx api error: %v", err)
		http.Error(w, "mediamtx unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, summary)
}

type camDetail struct {
	Cam           int      `json:"cam"`
	Path          string   `json:"path"`
	Ready         bool     `json:"ready"`
	Tracks        []string `json:"tracks"`
	BytesReceived uint64   `json:"bytesReceived"`
	Readers       int      `json:"readers"`
	Kind          string   `json:"kind,omitempty"`      // "embed" or "hls" for a non-RTSP vendor bus; omitted for real MediaMTX ingest
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
	for _, p := range paths {
		busID, cam, ok := parseBusPath(p.Name)
		if !ok || busID != id {
			continue
		}
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
func camsForBus(paths []mtxPath, busID string) []mtxPath {
	var out []mtxPath
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
	Kind      string `json:"kind,omitempty"`      // "embed" or "hls" — no MediaMTX ingest; DirectURL below carries the vendor's own link
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

// streamResultToInfo renders a just-started domain.StreamResult in the
// same shape handleStreamLive already uses for a cache-discovered entry.
func streamResultToInfo(r domain.StreamResult, cam int) streamInfo {
	si := streamInfo{Cam: cam, Path: r.Key, Ready: true}
	switch r.Kind {
	case domain.KindEmbed:
		si.Kind = "embed"
		si.DirectURL = r.EmbedURL
	case domain.KindHLS:
		si.Kind = "hls"
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
