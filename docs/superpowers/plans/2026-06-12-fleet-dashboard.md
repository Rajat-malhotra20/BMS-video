# Fleet Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Production-quality React dashboard for monitoring up to 3000 buses × 3 cameras: virtualized fleet list, per-bus live WebRTC/HLS playback, 10-minute rolling recordings.

**Architecture:** New React + Vite + TS SPA in `fleet-stack/dashboard`, served by the existing Go reverse-proxy backend in `prototype/backend`. Go gains a compact `/api/fleet` aggregation endpoint (cached, with lastSeen tracking), `/api/bus/{id}` detail, and a `/playback/` proxy to MediaMTX's playback server. MediaMTX gains fMP4 recording with 10-min auto-delete.

**Tech Stack:** React 18, Vite, TypeScript, Tailwind CSS, @tanstack/react-query, @tanstack/react-virtual, hls.js, Go 1.22 stdlib, MediaMTX, Vitest.

**Spec:** `docs/superpowers/specs/2026-06-12-fleet-dashboard-design.md`

**Conventions:**
- Stream path naming: `bus_<busId>_<camNo>` (e.g. `bus_12_3`), optionally prefixed (`live/bus_12_3`). Cameras 1–3.
- All shell commands run from repo root `C:\Users\RamanSharma\OneDrive - GNA-Energy\Desktop\BMS` unless stated.
- Windows: use PowerShell. `npm`, `go`, `docker` must be on PATH.

---

### Task 0: Initialize git repository

The BMS folder is not a git repo; plan requires frequent commits.

**Files:**
- Create: `.gitignore`

- [ ] **Step 1: Init repo**

```powershell
git init
```

- [ ] **Step 2: Create `.gitignore`**

```gitignore
node_modules/
dist/
*.log
.DS_Store
recordings/
```

- [ ] **Step 3: Initial commit**

```powershell
git add -A
git commit -m "chore: initial commit of existing prototype + specs"
```

---

### Task 1: MediaMTX recording + playback config

**Files:**
- Modify: `prototype/mediamtx_conf/mediamtx.yml`
- Modify: `prototype/docker-compose.yml`

- [ ] **Step 1: Add recording + playback settings to `mediamtx.yml`**

Insert after the `api: yes` line (line 9):

```yaml
playback: yes
playbackAddress: :9996
```

Replace the whole `paths:` block (lines 41–48) with:

```yaml
pathDefaults:
  record: yes
  recordPath: /recordings/%path/%Y-%m-%d_%H-%M-%S-%f
  recordFormat: fmp4
  recordPartDuration: 1s
  recordSegmentDuration: 60s
  recordDeleteAfter: 10m

paths:
  all:
    source: publisher
```

(`all` is MediaMTX's catch-all path name; every published path inherits `pathDefaults`, giving each camera a 10-minute rolling buffer.)

- [ ] **Step 2: Expose playback port + recordings volume in `docker-compose.yml`**

In the `mediamtx` service, add to `ports`:

```yaml
      - "19996:9996" # Playback (recordings)
```

and add to `volumes`:

```yaml
      - ./recordings:/recordings
```

- [ ] **Step 3: Verify config loads**

```powershell
cd prototype
docker compose up -d mediamtx
docker compose logs mediamtx --tail 20
```

Expected: log lines include `[playback] listener opened on :9996` and no `ERR` about config. Then:

```powershell
cd ..
```

- [ ] **Step 4: Commit**

```powershell
git add prototype/mediamtx_conf/mediamtx.yml prototype/docker-compose.yml
git commit -m "feat: enable 10-min rolling recording and playback server in MediaMTX"
```

---

### Task 2: Go fleet aggregation — path parsing + tests

**Files:**
- Create: `prototype/backend/fleet.go`
- Create: `prototype/backend/fleet_test.go`

- [ ] **Step 1: Write failing tests in `prototype/backend/fleet_test.go`**

```go
package main

import (
	"testing"
	"time"
)

func TestParseBusPath(t *testing.T) {
	cases := []struct {
		in    string
		busID string
		cam   int
		ok    bool
	}{
		{"bus_1_1", "1", 1, true},
		{"bus_42_3", "42", 3, true},
		{"live/bus_7_2", "7", 2, true},
		{"bus_1", "", 0, false},
		{"bus_1_x", "", 0, false},
		{"all_others", "", 0, false},
		{"bus__1", "", 0, false},
		{"bus_1_0", "", 0, false},  // cams are 1-based
		{"bus_1_99", "", 0, false}, // max 9 cams
	}
	for _, c := range cases {
		busID, cam, ok := parseBusPath(c.in)
		if busID != c.busID || cam != c.cam || ok != c.ok {
			t.Errorf("parseBusPath(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.in, busID, cam, ok, c.busID, c.cam, c.ok)
		}
	}
}

func TestBuildFleet(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	tracker := newFleetTracker()
	// bus 1: cams 1,2 online. bus 2: cam 1 online. junk ignored.
	fleet := tracker.build([]mtxPath{
		{Name: "bus_1_1", Ready: true},
		{Name: "bus_1_2", Ready: true},
		{Name: "bus_2_1", Ready: true},
		{Name: "not_a_bus", Ready: true},
		{Name: "bus_3_1", Ready: false}, // path exists but no publisher
	}, now)

	if len(fleet.Buses) != 2 {
		t.Fatalf("got %d buses, want 2", len(fleet.Buses))
	}
	if fleet.Totals.BusesOnline != 2 || fleet.Totals.CamsOnline != 3 {
		t.Fatalf("totals = %+v, want 2 buses / 3 cams", fleet.Totals)
	}
	if fleet.Buses[0].ID != "1" || len(fleet.Buses[0].Cams) != 2 {
		t.Fatalf("bus[0] = %+v, want id 1 with 2 cams", fleet.Buses[0])
	}

	// Bus 1 disappears: still listed (lastSeen) with no cams, within 10 min.
	fleet2 := tracker.build([]mtxPath{
		{Name: "bus_2_1", Ready: true},
	}, now.Add(2*time.Minute))
	if len(fleet2.Buses) != 2 {
		t.Fatalf("got %d buses after dropout, want 2 (one recently-seen)", len(fleet2.Buses))
	}
	var bus1 *fleetBus
	for i := range fleet2.Buses {
		if fleet2.Buses[i].ID == "1" {
			bus1 = &fleet2.Buses[i]
		}
	}
	if bus1 == nil || len(bus1.Cams) != 0 {
		t.Fatalf("bus 1 should be present with 0 cams, got %+v", bus1)
	}

	// After >10 min absent, bus 1 drops out entirely.
	fleet3 := tracker.build([]mtxPath{
		{Name: "bus_2_1", Ready: true},
	}, now.Add(11*time.Minute))
	if len(fleet3.Buses) != 1 {
		t.Fatalf("got %d buses after expiry, want 1", len(fleet3.Buses))
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```powershell
cd prototype/backend
go test ./...
```

Expected: compile error `undefined: parseBusPath` etc.

- [ ] **Step 3: Implement `prototype/backend/fleet.go`**

```go
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
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Tracks  []string `json:"tracks"`
	BytesReceived uint64 `json:"bytesReceived"`
	Readers []struct {
		Type string `json:"type"`
	} `json:"readers"`
}

type fleetBus struct {
	ID       string `json:"id"`
	Cams     []int  `json:"cams"`     // camera numbers currently live
	LastSeen int64  `json:"lastSeen"` // unix seconds any cam was last live
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

var busPathRe = regexp.MustCompile(`^(?:.*/)?bus_([0-9]+)_([1-9])$`)

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
```

- [ ] **Step 4: Run tests, verify pass**

```powershell
go test ./...
```

Expected: `ok` (PASS). Then `cd ../..`.

- [ ] **Step 5: Commit**

```powershell
git add prototype/backend/fleet.go prototype/backend/fleet_test.go
git commit -m "feat: bus path parser and fleet tracker with recently-seen window"
```

---

### Task 3: Go API endpoints — /api/fleet, /api/bus/{id}, /playback proxy

**Files:**
- Create: `prototype/backend/api.go`
- Create: `prototype/backend/api_test.go`
- Modify: `prototype/backend/main.go`
- Modify: `prototype/docker-compose.yml`

- [ ] **Step 1: Write failing test in `prototype/backend/api_test.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFleetHandler(t *testing.T) {
	// Fake MediaMTX API with two pages of paths.
	mtx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "" || page == "0" {
			fmt.Fprint(w, `{"pageCount":2,"items":[{"name":"bus_1_1","ready":true},{"name":"bus_1_2","ready":true}]}`)
		} else {
			fmt.Fprint(w, `{"pageCount":2,"items":[{"name":"bus_2_1","ready":true}]}`)
		}
	}))
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/fleet", nil)
	rec := httptest.NewRecorder()
	api.handleFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got fleetSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.Totals.BusesOnline != 2 || got.Totals.CamsOnline != 3 {
		t.Fatalf("totals = %+v, want 2 buses / 3 cams", got.Totals)
	}
}

func TestBusDetailHandler(t *testing.T) {
	mtx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"pageCount":1,"items":[
			{"name":"bus_1_1","ready":true,"tracks":["H264"],"bytesReceived":1000},
			{"name":"bus_1_2","ready":true,"tracks":["H264"],"bytesReceived":2000},
			{"name":"bus_2_1","ready":true}
		]}`)
	}))
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/bus/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	api.handleBusDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got busDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.ID != "1" || len(got.Cams) != 2 {
		t.Fatalf("detail = %+v, want bus 1 with 2 cams", got)
	}
	if got.Cams[0].Path != "bus_1_1" {
		t.Fatalf("cam[0].Path = %q, want bus_1_1", got.Cams[0].Path)
	}
}
```

- [ ] **Step 2: Run tests, verify fail**

```powershell
cd prototype/backend
go test ./...
```

Expected: compile error `undefined: newAPIServer`.

- [ ] **Step 3: Implement `prototype/backend/api.go`**

```go
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
```

- [ ] **Step 4: Run tests, verify pass**

```powershell
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Wire routes in `main.go`**

In `main.go`, add to the `config` struct (after `mediaMTXAPI`):

```go
	mediaMTXPlayback string
```

In `main()`, add to the `cfg` literal:

```go
		mediaMTXPlayback: env("MEDIAMTX_PLAYBACK_URL", "http://localhost:9996"),
```

After the `mux.Handle("/mtx-api/", ...)` line, add:

```go
	api := newAPIServer(cfg.mediaMTXAPI)
	mux.HandleFunc("GET /api/fleet", api.handleFleet)
	mux.HandleFunc("GET /api/bus/{id}", api.handleBusDetail)
	mux.Handle("/playback/", reverseProxy(cfg.mediaMTXPlayback, "/playback", noCache))
```

- [ ] **Step 6: Add env var to `docker-compose.yml` backend service**

```yaml
      MEDIAMTX_PLAYBACK_URL: http://mediamtx:9996
```

- [ ] **Step 7: Build + test**

```powershell
go build ./... && go test ./...
```

Expected: builds clean, tests PASS. Then `cd ../..`.

- [ ] **Step 8: Commit**

```powershell
git add prototype/backend prototype/docker-compose.yml
git commit -m "feat: /api/fleet, /api/bus/{id} and /playback proxy in backend"
```

---

### Task 4: Scaffold React app

**Files:**
- Create: `fleet-stack/dashboard/*` (Vite scaffold)
- Modify: `fleet-stack/dashboard/vite.config.ts`
- Create: `fleet-stack/dashboard/src/index.css` (replace scaffold)

- [ ] **Step 1: Scaffold**

```powershell
cd fleet-stack/dashboard
npm create vite@latest . -- --template react-ts
npm install
npm install @tanstack/react-query @tanstack/react-virtual hls.js
npm install -D tailwindcss @tailwindcss/vite vitest
```

- [ ] **Step 2: Configure `vite.config.ts`** (replace file)

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:4000',
      '/whep': 'http://localhost:4000',
      '/live': 'http://localhost:4000',
      '/playback': 'http://localhost:4000',
    },
  },
  test: {
    environment: 'node',
  },
})
```

Note: `test` key needs vitest types — change the first line to:

```ts
import { defineConfig } from 'vitest/config'
```

- [ ] **Step 3: Replace `src/index.css`** with Tailwind v4 entry + theme:

```css
@import "tailwindcss";

@theme {
  --color-surface-0: #0b0e14;
  --color-surface-1: #11151f;
  --color-surface-2: #1a2030;
  --color-edge: #232b3d;
  --color-ink: #e6e9f0;
  --color-ink-dim: #8b93a7;
  --color-live: #34d399;
  --color-warn: #fbbf24;
  --color-down: #f87171;
  --color-accent: #60a5fa;
}

html, body, #root {
  height: 100%;
  background: var(--color-surface-0);
  color: var(--color-ink);
  font-family: ui-sans-serif, system-ui, "Segoe UI", sans-serif;
}
```

- [ ] **Step 4: Clean scaffold cruft**

Delete `src/App.css`, `src/assets/react.svg`, `public/vite.svg`. Replace `src/App.tsx` temporarily with:

```tsx
export default function App() {
  return <div className="p-8 text-ink">Fleet dashboard scaffold OK</div>
}
```

- [ ] **Step 5: Verify dev server runs**

```powershell
npm run dev
```

Expected: opens on `http://localhost:5173`, dark page with text. Stop with Ctrl+C. Then `cd ../..`.

- [ ] **Step 6: Commit**

```powershell
git add fleet-stack/dashboard
git commit -m "feat: scaffold React+Vite+TS dashboard with Tailwind, Query, Virtual"
```

---

### Task 5: Types + path parser + fleet status logic (frontend)

**Files:**
- Create: `fleet-stack/dashboard/src/api/types.ts`
- Create: `fleet-stack/dashboard/src/lib/fleet.ts`
- Test: `fleet-stack/dashboard/src/lib/fleet.test.ts`

- [ ] **Step 1: Create `src/api/types.ts`**

```ts
export interface FleetBus {
  id: string
  cams: number[] // live camera numbers
  lastSeen: number // unix seconds
}

export interface FleetTotals {
  busesOnline: number
  busesSeen: number
  camsOnline: number
}

export interface FleetSummary {
  buses: FleetBus[]
  totals: FleetTotals
  updatedAt: number
}

export interface CamDetail {
  cam: number
  path: string
  ready: boolean
  tracks: string[]
  bytesReceived: number
  readers: number
}

export interface BusDetail {
  id: string
  cams: CamDetail[]
}

export type BusStatus = 'online' | 'partial' | 'offline'

export interface RecordingSegment {
  start: string // ISO timestamp
  duration: number // seconds
}
```

- [ ] **Step 2: Write failing tests `src/lib/fleet.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import { busStatus, filterBuses, CAMS_PER_BUS } from './fleet'
import type { FleetBus } from '../api/types'

const bus = (id: string, cams: number[]): FleetBus => ({ id, cams, lastSeen: 0 })

describe('busStatus', () => {
  it('all cams live -> online', () => {
    expect(busStatus(bus('1', [1, 2, 3]))).toBe('online')
  })
  it('some cams live -> partial', () => {
    expect(busStatus(bus('1', [1]))).toBe('partial')
  })
  it('no cams live -> offline', () => {
    expect(busStatus(bus('1', []))).toBe('offline')
  })
  it('CAMS_PER_BUS is 3', () => {
    expect(CAMS_PER_BUS).toBe(3)
  })
})

describe('filterBuses', () => {
  const buses = [bus('1', [1, 2, 3]), bus('12', [1]), bus('2', [])]
  it('search matches id substring', () => {
    expect(filterBuses(buses, '1', 'all').map(b => b.id)).toEqual(['1', '12'])
  })
  it('status filter online', () => {
    expect(filterBuses(buses, '', 'online').map(b => b.id)).toEqual(['1'])
  })
  it('status filter offline', () => {
    expect(filterBuses(buses, '', 'offline').map(b => b.id)).toEqual(['2'])
  })
  it('empty search + all returns everything', () => {
    expect(filterBuses(buses, '', 'all')).toHaveLength(3)
  })
})
```

- [ ] **Step 3: Run tests, verify fail**

```powershell
cd fleet-stack/dashboard
npx vitest run
```

Expected: FAIL — cannot resolve `./fleet`.

- [ ] **Step 4: Implement `src/lib/fleet.ts`**

```ts
import type { BusStatus, FleetBus } from '../api/types'

export const CAMS_PER_BUS = 3

export function busStatus(bus: FleetBus): BusStatus {
  if (bus.cams.length === 0) return 'offline'
  if (bus.cams.length >= CAMS_PER_BUS) return 'online'
  return 'partial'
}

export type StatusFilter = 'all' | BusStatus

export function filterBuses(
  buses: FleetBus[],
  search: string,
  filter: StatusFilter,
): FleetBus[] {
  const q = search.trim().toLowerCase()
  return buses.filter(b => {
    if (q && !b.id.toLowerCase().includes(q)) return false
    if (filter !== 'all' && busStatus(b) !== filter) return false
    return true
  })
}
```

- [ ] **Step 5: Run tests, verify pass**

```powershell
npx vitest run
```

Expected: all PASS. Then `cd ../..`.

- [ ] **Step 6: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: fleet types, status derivation and list filtering with tests"
```

---

### Task 6: Data hooks (polling)

**Files:**
- Create: `fleet-stack/dashboard/src/api/client.ts`
- Create: `fleet-stack/dashboard/src/hooks/useFleet.ts`
- Create: `fleet-stack/dashboard/src/hooks/useBusDetail.ts`
- Create: `fleet-stack/dashboard/src/hooks/useRecordings.ts`

- [ ] **Step 1: Create `src/api/client.ts`**

```ts
export async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`)
  return res.json() as Promise<T>
}
```

- [ ] **Step 2: Create `src/hooks/useFleet.ts`**

```ts
import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { FleetSummary } from '../api/types'

export const FLEET_POLL_MS = 5000

export function useFleet() {
  return useQuery({
    queryKey: ['fleet'],
    queryFn: () => getJSON<FleetSummary>('/api/fleet'),
    refetchInterval: FLEET_POLL_MS,
    staleTime: FLEET_POLL_MS,
    placeholderData: prev => prev, // keep last data on refetch error
  })
}
```

- [ ] **Step 3: Create `src/hooks/useBusDetail.ts`**

```ts
import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { BusDetail } from '../api/types'

export function useBusDetail(busId: string | null) {
  return useQuery({
    queryKey: ['bus', busId],
    queryFn: () => getJSON<BusDetail>(`/api/bus/${busId}`),
    enabled: busId !== null,
    refetchInterval: 5000,
    placeholderData: prev => prev,
  })
}
```

- [ ] **Step 4: Create `src/hooks/useRecordings.ts`**

MediaMTX playback server: `GET /playback/list?path=<name>` returns `[{"start":"...","duration":...}]`; a segment is played via `GET /playback/get?path=<name>&start=<iso>&duration=<sec>&format=mp4`.

```ts
import { useQuery } from '@tanstack/react-query'
import { getJSON } from '../api/client'
import type { RecordingSegment } from '../api/types'

export function useRecordings(path: string | null) {
  return useQuery({
    queryKey: ['recordings', path],
    queryFn: () =>
      getJSON<RecordingSegment[]>(
        `/playback/list?path=${encodeURIComponent(path!)}`,
      ),
    enabled: path !== null,
    refetchInterval: 15000,
  })
}

export function segmentURL(path: string, seg: RecordingSegment): string {
  const params = new URLSearchParams({
    path,
    start: seg.start,
    duration: String(seg.duration),
    format: 'mp4',
  })
  return `/playback/get?${params}`
}
```

- [ ] **Step 5: Typecheck**

```powershell
cd fleet-stack/dashboard
npx tsc --noEmit
cd ../..
```

Expected: no errors.

- [ ] **Step 6: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: polling hooks for fleet, bus detail and recordings"
```

---

### Task 7: WHEP player with HLS fallback

**Files:**
- Create: `fleet-stack/dashboard/src/lib/whep.ts`
- Create: `fleet-stack/dashboard/src/components/VideoTile.tsx`

- [ ] **Step 1: Create `src/lib/whep.ts`** — minimal WHEP client

```ts
// Minimal WHEP (WebRTC-HTTP Egress Protocol) client for MediaMTX.
export interface WhepSession {
  pc: RTCPeerConnection
  close: () => void
}

export async function startWhep(
  path: string,
  video: HTMLVideoElement,
  signal: AbortSignal,
): Promise<WhepSession> {
  const pc = new RTCPeerConnection()
  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })

  pc.ontrack = ev => {
    if (video.srcObject !== ev.streams[0]) {
      video.srcObject = ev.streams[0]
    }
  }

  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)

  // Wait for ICE gathering (MediaMTX expects a complete offer).
  await new Promise<void>(resolve => {
    if (pc.iceGatheringState === 'complete') return resolve()
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check)
        resolve()
      }
    }
    pc.addEventListener('icegatheringstatechange', check)
    setTimeout(resolve, 1000) // cap gathering wait
  })

  const res = await fetch(`/whep/${path}/whep`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: pc.localDescription!.sdp,
    signal,
  })
  if (!res.ok) {
    pc.close()
    throw new Error(`WHEP ${path}: HTTP ${res.status}`)
  }
  await pc.setRemoteDescription({ type: 'answer', sdp: await res.text() })

  return {
    pc,
    close: () => {
      video.srcObject = null
      pc.close()
    },
  }
}
```

- [ ] **Step 2: Create `src/components/VideoTile.tsx`** — state machine: `connecting → live(webrtc) | live(hls) → reconnecting`, WHEP first, HLS fallback if no frame in 3 s, exponential backoff retry, full cleanup on unmount.

```tsx
import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { startWhep, type WhepSession } from '../lib/whep'

type TileState = 'connecting' | 'live-webrtc' | 'live-hls' | 'offline'

interface Props {
  path: string // e.g. bus_1_2
  label: string
  ready: boolean // from /api/bus poll — stream currently published
  onClick?: () => void
  large?: boolean
}

const FIRST_FRAME_TIMEOUT_MS = 3000

export default function VideoTile({ path, label, ready, onClick, large }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const [state, setState] = useState<TileState>('connecting')

  useEffect(() => {
    const video = videoRef.current
    if (!video || !ready) {
      setState('offline')
      return
    }

    let cancelled = false
    let whep: WhepSession | null = null
    let hls: Hls | null = null
    let retryTimer: number | undefined
    let attempt = 0
    const abort = new AbortController()

    const cleanupPlayers = () => {
      whep?.close()
      whep = null
      hls?.destroy()
      hls = null
    }

    const scheduleRetry = () => {
      if (cancelled) return
      cleanupPlayers()
      setState('connecting')
      attempt += 1
      const delay = Math.min(1000 * 2 ** attempt, 15000)
      retryTimer = window.setTimeout(start, delay)
    }

    const startHls = () => {
      if (cancelled) return
      cleanupPlayers()
      const src = `/live/${path}/index.m3u8`
      if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = src
        video.play().catch(() => {})
        setState('live-hls')
        return
      }
      hls = new Hls({ lowLatencyMode: true })
      hls.loadSource(src)
      hls.attachMedia(video)
      hls.on(Hls.Events.FRAG_BUFFERED, () => !cancelled && setState('live-hls'))
      hls.on(Hls.Events.ERROR, (_e, data) => {
        if (data.fatal) scheduleRetry()
      })
      video.play().catch(() => {})
    }

    const start = async () => {
      if (cancelled) return
      setState('connecting')
      const frameTimer = window.setTimeout(startHls, FIRST_FRAME_TIMEOUT_MS)
      try {
        whep = await startWhep(path, video, abort.signal)
        video.onplaying = () => {
          window.clearTimeout(frameTimer)
          if (!cancelled) {
            attempt = 0
            setState('live-webrtc')
          }
        }
        whep.pc.onconnectionstatechange = () => {
          const s = whep?.pc.connectionState
          if (s === 'failed' || s === 'disconnected') scheduleRetry()
        }
        video.play().catch(() => {})
      } catch {
        window.clearTimeout(frameTimer)
        startHls()
      }
    }

    start()
    return () => {
      cancelled = true
      abort.abort()
      window.clearTimeout(retryTimer)
      cleanupPlayers()
      video.onplaying = null
    }
  }, [path, ready])

  const badge =
    state === 'live-webrtc' ? '● LIVE' :
    state === 'live-hls' ? '● LIVE (HLS)' :
    state === 'connecting' ? '… connecting' : 'offline'

  const badgeColor =
    state === 'live-webrtc' || state === 'live-hls'
      ? 'text-live' : state === 'connecting' ? 'text-warn' : 'text-down'

  return (
    <div
      onClick={onClick}
      className={`relative overflow-hidden rounded-lg border border-edge bg-surface-2 ${
        onClick ? 'cursor-pointer hover:border-accent' : ''
      } ${large ? 'col-span-3' : ''}`}
    >
      <video
        ref={videoRef}
        muted
        playsInline
        autoPlay
        className="aspect-video w-full bg-black object-contain"
      />
      <div className="absolute left-2 top-2 rounded bg-black/60 px-2 py-0.5 text-xs font-medium">
        {label}
      </div>
      <div className={`absolute right-2 top-2 rounded bg-black/60 px-2 py-0.5 text-xs font-semibold ${badgeColor}`}>
        {badge}
      </div>
      {state === 'offline' && (
        <div className="absolute inset-0 grid place-items-center text-sm text-ink-dim">
          no signal
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Typecheck**

```powershell
cd fleet-stack/dashboard
npx tsc --noEmit
cd ../..
```

Expected: clean.

- [ ] **Step 4: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: WHEP video tile with HLS fallback and reconnect backoff"
```

---

### Task 8: Virtualized fleet list

**Files:**
- Create: `fleet-stack/dashboard/src/components/FleetList.tsx`

- [ ] **Step 1: Create `src/components/FleetList.tsx`**

```tsx
import { useVirtualizer } from '@tanstack/react-virtual'
import { memo, useMemo, useRef, useState } from 'react'
import type { FleetBus } from '../api/types'
import { busStatus, CAMS_PER_BUS, filterBuses, type StatusFilter } from '../lib/fleet'

const STATUS_DOT: Record<string, string> = {
  online: 'bg-live',
  partial: 'bg-warn',
  offline: 'bg-down',
}

const FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'online', label: 'Online' },
  { key: 'partial', label: 'Partial' },
  { key: 'offline', label: 'Offline' },
]

const BusRow = memo(function BusRow({
  bus, selected, onSelect,
}: { bus: FleetBus; selected: boolean; onSelect: (id: string) => void }) {
  const status = busStatus(bus)
  return (
    <button
      onClick={() => onSelect(bus.id)}
      className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors ${
        selected ? 'bg-surface-2 ring-1 ring-accent' : 'hover:bg-surface-1'
      }`}
    >
      <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${STATUS_DOT[status]}`} />
      <span className="flex-1 truncate font-medium">bus_{bus.id}</span>
      <span className="text-xs text-ink-dim">{bus.cams.length}/{CAMS_PER_BUS}</span>
    </button>
  )
})

interface Props {
  buses: FleetBus[]
  selectedId: string | null
  onSelect: (id: string) => void
}

export default function FleetList({ buses, selectedId, onSelect }: Props) {
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<StatusFilter>('all')
  const parentRef = useRef<HTMLDivElement>(null)

  const visible = useMemo(
    () => filterBuses(buses, search, filter),
    [buses, search, filter],
  )

  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 40,
    overscan: 10,
  })

  return (
    <div className="flex h-full flex-col gap-2 p-3">
      <input
        value={search}
        onChange={e => setSearch(e.target.value)}
        placeholder="Search bus id…"
        className="rounded-md border border-edge bg-surface-1 px-3 py-2 text-sm outline-none placeholder:text-ink-dim focus:border-accent"
      />
      <div className="flex gap-1">
        {FILTERS.map(f => (
          <button
            key={f.key}
            onClick={() => setFilter(f.key)}
            className={`rounded-full px-3 py-1 text-xs transition-colors ${
              filter === f.key
                ? 'bg-accent/20 text-accent'
                : 'text-ink-dim hover:bg-surface-1'
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>
      <div className="text-xs text-ink-dim">{visible.length} buses</div>
      <div ref={parentRef} className="min-h-0 flex-1 overflow-y-auto">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map(row => {
            const bus = visible[row.index]
            return (
              <div
                key={bus.id}
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  transform: `translateY(${row.start}px)`,
                }}
              >
                <BusRow bus={bus} selected={bus.id === selectedId} onSelect={onSelect} />
              </div>
            )
          })}
        </div>
        {visible.length === 0 && (
          <div className="p-6 text-center text-sm text-ink-dim">
            No buses match. Waiting for streams…
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Typecheck**

```powershell
cd fleet-stack/dashboard
npx tsc --noEmit
cd ../..
```

- [ ] **Step 3: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: virtualized searchable fleet list"
```

---

### Task 9: Bus detail with live tiles + recordings tab

**Files:**
- Create: `fleet-stack/dashboard/src/components/BusDetail.tsx`
- Create: `fleet-stack/dashboard/src/components/RecordingsPanel.tsx`

- [ ] **Step 1: Create `src/components/RecordingsPanel.tsx`**

```tsx
import { useState } from 'react'
import { useRecordings, segmentURL } from '../hooks/useRecordings'

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString()
}

function CamRecordings({ path, label }: { path: string; label: string }) {
  const { data: segments, isError } = useRecordings(path)
  const [playing, setPlaying] = useState<string | null>(null)

  return (
    <div className="rounded-lg border border-edge bg-surface-1 p-3">
      <div className="mb-2 text-sm font-semibold">{label}</div>
      {isError && <div className="text-xs text-down">recordings unavailable</div>}
      {segments && segments.length === 0 && (
        <div className="text-xs text-ink-dim">no recordings in last 10 min</div>
      )}
      <div className="flex flex-wrap gap-2">
        {segments?.map(seg => {
          const url = segmentURL(path, seg)
          return (
            <div key={seg.start} className="flex items-center gap-1">
              <button
                onClick={() => setPlaying(playing === url ? null : url)}
                className={`rounded px-2 py-1 text-xs transition-colors ${
                  playing === url
                    ? 'bg-accent/20 text-accent'
                    : 'bg-surface-2 text-ink-dim hover:text-ink'
                }`}
              >
                ⏵ {fmtTime(seg.start)} · {Math.round(seg.duration)}s
              </button>
              <a
                href={url}
                download={`${path}_${seg.start}.mp4`}
                className="text-xs text-ink-dim hover:text-accent"
                title="Download"
              >
                ⬇
              </a>
            </div>
          )
        })}
      </div>
      {playing && (
        <video
          key={playing}
          src={playing}
          controls
          autoPlay
          className="mt-3 aspect-video w-full rounded bg-black"
        />
      )}
    </div>
  )
}

export default function RecordingsPanel({ busId, paths }: { busId: string; paths: { cam: number; path: string }[] }) {
  return (
    <div className="flex flex-col gap-3">
      {paths.map(p => (
        <CamRecordings key={p.path} path={p.path} label={`Bus ${busId} — Camera ${p.cam}`} />
      ))}
      {paths.length === 0 && (
        <div className="text-sm text-ink-dim">No cameras known for this bus yet.</div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Create `src/components/BusDetail.tsx`**

```tsx
import { useState } from 'react'
import { useBusDetail } from '../hooks/useBusDetail'
import { CAMS_PER_BUS } from '../lib/fleet'
import RecordingsPanel from './RecordingsPanel'
import VideoTile from './VideoTile'

function fmtBitrate(bytes: number): string {
  if (bytes <= 0) return '—'
  const mb = bytes / 1_000_000
  return mb >= 1000 ? `${(mb / 1000).toFixed(1)} GB` : `${mb.toFixed(1)} MB`
}

export default function BusDetail({ busId }: { busId: string }) {
  const { data, isError } = useBusDetail(busId)
  const [tab, setTab] = useState<'live' | 'recordings'>('live')
  const [expanded, setExpanded] = useState<number | null>(null)

  // Always render CAMS_PER_BUS slots so layout is stable.
  const slots = Array.from({ length: CAMS_PER_BUS }, (_, i) => {
    const cam = i + 1
    const detail = data?.cams.find(c => c.cam === cam)
    return { cam, path: detail?.path ?? `bus_${busId}_${cam}`, detail }
  })
  const liveCount = data?.cams.filter(c => c.ready).length ?? 0

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold tracking-wide">BUS_{busId}</h1>
        <span className={`text-sm font-medium ${liveCount > 0 ? 'text-live' : 'text-down'}`}>
          ● {liveCount}/{CAMS_PER_BUS} LIVE
        </span>
      </div>

      <div className="flex gap-1">
        {(['live', 'recordings'] as const).map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`rounded-md px-4 py-1.5 text-sm transition-colors ${
              tab === t ? 'bg-accent/20 text-accent' : 'text-ink-dim hover:bg-surface-1'
            }`}
          >
            {t === 'live' ? '● Live' : '⏪ Last 10 min'}
          </button>
        ))}
      </div>

      {isError && (
        <div className="rounded-md border border-down/40 bg-down/10 p-3 text-sm text-down">
          Bus data unavailable — backend unreachable. Retrying…
        </div>
      )}

      {tab === 'live' && (
        <>
          <div className={`grid gap-3 ${expanded !== null ? 'grid-cols-3' : 'grid-cols-3'}`}>
            {expanded !== null && (() => {
              const s = slots[expanded - 1]
              return (
                <VideoTile
                  key={`big-${s.path}`}
                  path={s.path}
                  label={`CAM ${s.cam}`}
                  ready={s.detail?.ready ?? false}
                  large
                  onClick={() => setExpanded(null)}
                />
              )
            })()}
            {slots
              .filter(s => s.cam !== expanded)
              .map(s => (
                <VideoTile
                  key={s.path}
                  path={s.path}
                  label={`CAM ${s.cam}`}
                  ready={s.detail?.ready ?? false}
                  onClick={() => setExpanded(expanded === s.cam ? null : s.cam)}
                />
              ))}
          </div>
          <div className="grid grid-cols-3 gap-3 text-xs text-ink-dim">
            {slots.map(s => (
              <div key={s.path} className="rounded-md border border-edge bg-surface-1 p-2">
                <div className="font-medium text-ink">CAM {s.cam}</div>
                <div>codec: {s.detail?.tracks?.join(', ') || '—'}</div>
                <div>received: {fmtBitrate(s.detail?.bytesReceived ?? 0)}</div>
                <div>viewers: {s.detail?.readers ?? 0}</div>
              </div>
            ))}
          </div>
        </>
      )}

      {tab === 'recordings' && (
        <RecordingsPanel
          busId={busId}
          paths={slots.map(s => ({ cam: s.cam, path: s.path }))}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 3: Typecheck**

```powershell
cd fleet-stack/dashboard
npx tsc --noEmit
cd ../..
```

- [ ] **Step 4: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: bus detail with live tiles, expand, stats and recordings tab"
```

---

### Task 10: App shell — header KPIs + layout

**Files:**
- Create: `fleet-stack/dashboard/src/components/Header.tsx`
- Modify: `fleet-stack/dashboard/src/App.tsx`
- Modify: `fleet-stack/dashboard/src/main.tsx`

- [ ] **Step 1: Create `src/components/Header.tsx`**

```tsx
import type { FleetSummary } from '../api/types'

interface Props {
  fleet: FleetSummary | undefined
  isStale: boolean
}

export default function Header({ fleet, isStale }: Props) {
  return (
    <header className="flex items-center gap-6 border-b border-edge bg-surface-1 px-5 py-3">
      <div className="flex items-center gap-2 text-base font-bold tracking-widest">
        <span className={isStale ? 'text-down' : 'text-live'}>◉</span> FLEET BMS
      </div>
      <div className="flex gap-6 text-sm text-ink-dim">
        <span>
          buses online{' '}
          <strong className="text-ink">{fleet?.totals.busesOnline ?? '—'}</strong>
          <span className="text-ink-dim">/{fleet?.totals.busesSeen ?? '—'}</span>
        </span>
        <span>
          cams online <strong className="text-ink">{fleet?.totals.camsOnline ?? '—'}</strong>
        </span>
      </div>
      <div className="ml-auto text-xs text-ink-dim">
        {isStale
          ? `⚠ data stale since ${fleet ? new Date(fleet.updatedAt * 1000).toLocaleTimeString() : '—'}`
          : '⟳ 5s'}
      </div>
    </header>
  )
}
```

- [ ] **Step 2: Replace `src/App.tsx`**

```tsx
import { useState } from 'react'
import BusDetail from './components/BusDetail'
import FleetList from './components/FleetList'
import Header from './components/Header'
import { useFleet } from './hooks/useFleet'

export default function App() {
  const { data: fleet, isError } = useFleet()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  return (
    <div className="flex h-full flex-col">
      <Header fleet={fleet} isStale={isError} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-72 shrink-0 border-r border-edge bg-surface-0">
          <FleetList
            buses={fleet?.buses ?? []}
            selectedId={selectedId}
            onSelect={setSelectedId}
          />
        </aside>
        <main className="min-w-0 flex-1">
          {selectedId ? (
            <BusDetail busId={selectedId} />
          ) : (
            <div className="grid h-full place-items-center text-ink-dim">
              <div className="text-center">
                <div className="mb-2 text-4xl">🛰</div>
                <div className="text-sm">
                  Select a bus to view its cameras
                </div>
              </div>
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Replace `src/main.tsx`**

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'

const queryClient = new QueryClient({
  defaultQueries: undefined, // placeholder removed below
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
```

Correction — `QueryClient` options object has no `defaultQueries` key; use exactly:

```tsx
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 2 } },
})
```

- [ ] **Step 4: Run unit tests + typecheck + dev smoke**

```powershell
cd fleet-stack/dashboard
npx vitest run
npx tsc --noEmit
npm run dev
```

With backend running (`docker compose up -d` in `prototype/`), open `http://localhost:5173`: header KPIs render, list empty or populated, no console errors. Ctrl+C, `cd ../..`.

- [ ] **Step 5: Commit**

```powershell
git add fleet-stack/dashboard/src
git commit -m "feat: app shell with header KPIs, fleet pane and detail pane"
```

---

### Task 11: Production build served by Go backend (Docker integration)

**Files:**
- Modify: `prototype/backend/Dockerfile`
- Modify: `prototype/docker-compose.yml`

- [ ] **Step 1: Replace `prototype/backend/Dockerfile`** (build context moves to repo root so it can reach `fleet-stack/dashboard`):

```dockerfile
FROM node:20-alpine AS ui
WORKDIR /ui
COPY fleet-stack/dashboard/package*.json ./
RUN npm ci
COPY fleet-stack/dashboard ./
RUN npm run build

FROM golang:1.22-alpine AS build
WORKDIR /src/backend
COPY prototype/backend/go.mod ./
COPY prototype/backend/*.go ./
RUN go build -trimpath -ldflags="-s -w" -o /out/mediamtx-console

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/mediamtx-console /app/mediamtx-console
COPY --from=ui /ui/dist /app/frontend
EXPOSE 8080
ENTRYPOINT ["/app/mediamtx-console"]
```

- [ ] **Step 2: Update `docker-compose.yml` backend build section**

```yaml
  backend:
    build:
      context: ..
      dockerfile: prototype/backend/Dockerfile
```

(rest of the service unchanged)

- [ ] **Step 3: Build and run full stack**

```powershell
cd prototype
docker compose up -d --build
```

Expected: builds UI + Go, both containers `Up` in `docker compose ps`.

- [ ] **Step 4: Verify**

Open `http://localhost:4000` → new dark dashboard loads, header shows `◉ FLEET BMS`, KPIs `0/0` (no streams yet). `cd ..`.

- [ ] **Step 5: Commit**

```powershell
git add prototype/backend/Dockerfile prototype/docker-compose.yml
git commit -m "feat: multi-stage docker build serving React dashboard from Go backend"
```

---

### Task 12: End-to-end smoke test with simulated buses

**Files:** none (manual verification)

- [ ] **Step 1: Publish 3 cameras for bus 1 + 1 camera for bus 2** (4 PowerShell windows, or use `Start-Process`; needs FFmpeg):

```powershell
ffmpeg -re -f lavfi -i "testsrc=size=1280x720:rate=25,drawtext=text='BUS 1 CAM 1':fontsize=60:fontcolor=white:x=40:y=40" -c:v libx264 -preset veryfast -tune zerolatency -g 50 -f flv rtmp://localhost:11935/bus_1_1
```

Repeat with `bus_1_2`, `bus_1_3`, `bus_2_1` (change drawtext label to match).

- [ ] **Step 2: Verify fleet list**

`http://localhost:4000` → within 5 s: 2 buses listed; bus_1 green `3/3`, bus_2 amber `1/3`; header `buses online 2/2`, `cams online 4`.

- [ ] **Step 3: Verify live playback**

Click bus_1 → 3 tiles play (badge `● LIVE` = WebRTC; `● LIVE (HLS)` = fallback). Click a tile → expands. Search `2` in list → only bus_2 (and any id containing 2).

- [ ] **Step 4: Verify recordings**

Wait ≥ 2 min, click `⏪ Last 10 min` tab → segments listed per camera, click one → plays, download works.

- [ ] **Step 5: Verify dropout handling**

Stop the `bus_2_1` ffmpeg (Ctrl+C) → within ~15 s bus_2 turns red `0/3` in list; its tile (if open) shows `no signal`. Restart ffmpeg → recovers automatically.

- [ ] **Step 6: Verify scale behavior (lightweight)**

Frontend perf at 3000 buses is exercised by the virtualizer regardless of real streams; optionally spot-check list smoothness by temporarily mocking: in browser devtools network tab confirm `/api/fleet` payload stays small and list scroll stays smooth.

- [ ] **Step 7: Final commit + tag**

```powershell
git add -A
git commit -m "chore: smoke-tested fleet dashboard v1" --allow-empty
```

---

## Self-review notes

- Spec coverage: list+detail (T8/T9), WHEP+HLS fallback (T7), recordings 10-min (T1/T9), `/api/fleet` compact + cache (T3), virtualization (T8), KPIs+stale banner (T10), docker serving (T11), tests (T2/T3/T5), smoke (T12). Recently-seen red status (T2 tracker). Covered.
- Known judgment calls: `bytesReceived` shown as total received (not instantaneous bitrate) — acceptable v1; MediaMTX playback `list` response shape verified against MediaMTX v1.x docs at implementation time (Task 9 Step 1 notes endpoint contract — if response differs, adjust `RecordingSegment` accordingly).
