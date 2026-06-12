package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFleetHandler(t *testing.T) {
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
