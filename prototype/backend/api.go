package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type apiServer struct {
	mtxAPIBase string // e.g. http://mediamtx:9997/v3
	tracker    *fleetTracker
	client     *http.Client

	cacheMu     sync.Mutex
	cachedFleet *fleetSummary
	cachedPaths []mtxPath
	cachedAt    time.Time
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
		var list mtxPathList
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, list.Items...)
		if page >= list.PageCount-1 {
			break
		}
	}
	return all, nil
}

// snapshot returns cached paths+summary, refreshing from MediaMTX when stale.
func (a *apiServer) snapshot() (*fleetSummary, []mtxPath, error) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	if a.cachedFleet != nil && time.Since(a.cachedAt) < fleetCacheTTL {
		return a.cachedFleet, a.cachedPaths, nil
	}
	paths, err := a.fetchAllPaths()
	if err != nil {
		return nil, nil, err
	}
	summary := a.tracker.build(paths, time.Now())
	a.cachedFleet = &summary
	a.cachedPaths = paths
	a.cachedAt = time.Now()
	return a.cachedFleet, a.cachedPaths, nil
}

func (a *apiServer) handleFleet(w http.ResponseWriter, _ *http.Request) {
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
		detail.Cams = append(detail.Cams, camDetail{
			Cam:           cam,
			Path:          p.Name,
			Ready:         p.Ready,
			Tracks:        p.Tracks,
			BytesReceived: p.BytesReceived,
			Readers:       len(p.Readers),
		})
	}
	writeJSON(w, detail)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
