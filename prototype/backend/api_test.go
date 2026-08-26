package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"mediamtx-console/domain"
)

func TestFleetHandler(t *testing.T) {
	mtx := fakeIngest(`[
		{"name":"DL1PC0001_1","ready":true},
		{"name":"DL1PC0001_2","ready":true},
		{"name":"DL1PC0002_1","ready":true}
	]`)
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

// A dead ingest service must NOT fail the fleet. It used to (502), back when
// every camera was remuxed through MediaMTX and no paths meant no fleet.
// That is no longer true: Chemito (HTTP-FLV) and Sumith (HLS/embed) never
// touch a media server, so a deployment can run with no MediaMTX and no
// media-mtxd at all. Failing here would blank a fleet that is entirely fine.
func TestFleetHandlerIngestDown(t *testing.T) {
	// Start then immediately close a server so its URL is valid but unreachable.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	api := newAPIServer(closedURL)
	req := httptest.NewRequest("GET", "/api/fleet", nil)
	rec := httptest.NewRecorder()
	api.handleFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded, not failed)", rec.Code)
	}
	var got fleetSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.Totals.CamsOnline != 0 {
		t.Fatalf("camsOnline = %d, want 0 with nothing ingesting", got.Totals.CamsOnline)
	}
}

// The same again with no ingest service configured at all (INGEST_URL=""),
// which is how a MediaMTX-free deployment is meant to be run: no attempt, no
// log noise, still a working fleet.
func TestFleetHandlerNoIngestConfigured(t *testing.T) {
	api := newAPIServer("")
	req := httptest.NewRequest("GET", "/api/fleet", nil)
	rec := httptest.NewRecorder()
	api.handleFleet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if api.ingest != nil {
		t.Error("ingest client built for an empty base URL; want none")
	}
}

func TestBusDetailHandler(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/bus/DL1PC0001", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleBusDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got busDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.ID != "DL1PC0001" || len(got.Cams) != 2 {
		t.Fatalf("detail = %+v, want bus DL1PC0001 with 2 cams", got)
	}
	if got.Cams[0].Path != "DL1PC0001_1" {
		t.Fatalf("cam[0].Path = %q, want DL1PC0001_1", got.Cams[0].Path)
	}
}

// fakeIngest stands in for the media-MTX service: GET /paths returning a
// flat array. This API no longer speaks to MediaMTX, so there is no paged
// {"pageCount":N,"items":[...]} envelope to emulate any more.
func fakeIngest(pathsJSON string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/paths" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, pathsJSON)
	}))
}

func streamTestMtx() *httptest.Server {
	return fakeIngest(`[
		{"name":"DL1PC0001_1","ready":true,"tracks":["H264"],"bytesReceived":1000},
		{"name":"DL1PC0001_2","ready":true,"tracks":["H264"],"bytesReceived":2000},
		{"name":"DL1PC0002_1","ready":true}
	]`)
}

func TestStreamLiveHandler_AllCams(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamLive(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []streamInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Path != "DL1PC0001_1" || got[0].WhepURL != "/whep/DL1PC0001_1/whep" || got[0].HLSURL != "/live/DL1PC0001_1/index.m3u8" {
		t.Fatalf("got[0] = %+v, unexpected", got[0])
	}
}

func TestStreamLiveHandler_SingleCam(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001?cam=2", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamLive(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []streamInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(got) != 1 || got[0].Path != "DL1PC0001_2" {
		t.Fatalf("got = %+v, want single DL1PC0001_2", got)
	}
}

func TestStreamLiveHandler_CamNotFound(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001?cam=9", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamLive(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "[]\n" {
		t.Fatalf("body = %q, want []", rec.Body.String())
	}
}

func TestStreamRecordingHandler_Valid(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001/recording?from=2026-01-01T00:00:00Z&to=2026-01-01T00:02:00Z", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamRecording(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []recordingInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	for _, r := range got {
		if !strings.Contains(r.URL, "duration=120") {
			t.Fatalf("url %q missing duration=120", r.URL)
		}
	}
	if !strings.Contains(got[0].URL, "path=DL1PC0001_1") && !strings.Contains(got[1].URL, "path=DL1PC0001_1") {
		t.Fatalf("no url contains path=DL1PC0001_1: %+v", got)
	}
	if !strings.Contains(got[0].URL, "path=DL1PC0001_2") && !strings.Contains(got[1].URL, "path=DL1PC0001_2") {
		t.Fatalf("no url contains path=DL1PC0001_2: %+v", got)
	}
}

func TestStreamRecordingHandler_InvalidRange(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001/recording?from=2026-01-01T00:02:00Z&to=2026-01-01T00:00:00Z", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamRecording(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStreamRecordingHandler_MissingParams(t *testing.T) {
	mtx := streamTestMtx()
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001/recording?to=2026-01-01T00:00:00Z", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamRecording(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A bare GET /api/stream/{id} must bring up every channel, not just ones
// already running — that's the whole point of hitting a bus id: one call,
// all cams. Cam 1 here is already live in the snapshot; 2-4 are not and
// have to be started.
func TestStreamLiveHandler_AllCamsStartsMissing(t *testing.T) {
	mtx := fakeIngest(`[{"name":"DL1PC0001_1","ready":true}]`)
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	var started []int
	api.ensureStream = func(_ context.Context, bus string, cam int) (*domain.StreamResult, error) {
		started = append(started, cam)
		if cam == 3 {
			return nil, fmt.Errorf("camera %d is dead", cam) // must not blank the others
		}
		return &domain.StreamResult{
			Key:    bus + "_" + strconv.Itoa(cam),
			Kind:   domain.KindFLV,
			HLSURL: "/api/flv/" + bus + "_" + strconv.Itoa(cam),
		}, nil
	}

	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001", nil)
	req.SetPathValue("id", "DL1PC0001")
	rec := httptest.NewRecorder()
	api.handleStreamLive(rec, req)

	var got []streamInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(started) != 3 || started[0] != 2 || started[2] != 4 {
		t.Fatalf("started = %v, want cams 2,3,4 (1 was already live)", started)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (cam 3 failed to start)", len(got))
	}
	for i, wantCam := range []int{1, 2, 4} {
		if got[i].Cam != wantCam {
			t.Fatalf("got[%d].Cam = %d, want %d (sorted)", i, got[i].Cam, wantCam)
		}
	}
	if got[1].Kind != "flv" || got[1].DirectURL != "/api/flv/DL1PC0001_2" {
		t.Fatalf("got[1] = %+v, want the started FLV entry", got[1])
	}
}

// The fan-out must follow the vendor's own channel count, not a fixed 4 —
// Chemito reports 9 per DVR and 7 of them really stream. Capping at 4 hid
// three working cameras.
func TestStreamLiveHandler_AllCamsUsesVendorChannelCount(t *testing.T) {
	mtx := fakeIngest(`[]`)
	defer mtx.Close()

	api := newAPIServer(mtx.URL)
	api.channelCounts = func(context.Context) map[string]int { return map[string]int{"DL1PC0001": 9} }
	var started []int
	api.ensureStream = func(_ context.Context, bus string, cam int) (*domain.StreamResult, error) {
		started = append(started, cam)
		return &domain.StreamResult{Key: bus + "_" + strconv.Itoa(cam), Kind: domain.KindFLV}, nil
	}

	req := httptest.NewRequest("GET", "/api/stream/DL1PC0001", nil)
	req.SetPathValue("id", "DL1PC0001")
	api.handleStreamLive(httptest.NewRecorder(), req)

	if len(started) != 9 {
		t.Fatalf("started %d cams (%v), want all 9 the vendor reports", len(started), started)
	}
}

// Cam numbers past 9 must parse, or a bigger DVR silently loses channels.
func TestParseBusPathTwoDigitCam(t *testing.T) {
	bus, cam, ok := parseBusPath("DL1PC0001_12")
	if !ok || bus != "DL1PC0001" || cam != 12 {
		t.Fatalf("parseBusPath = (%q, %d, %v), want (DL1PC0001, 12, true)", bus, cam, ok)
	}
	if _, _, ok := parseBusPath("DL1PC0001_01"); ok {
		t.Fatalf("leading-zero cam should not parse")
	}
}
