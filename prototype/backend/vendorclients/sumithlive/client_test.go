package sumithlive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTokenAndLiveStreamingLink(t *testing.T) {
	var gotAuthCode string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("token") {
		case "generateAccessToken":
			var body struct{ Username, Password string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Username != "nishant.dhaktod@sumith.in" {
				t.Fatalf("unexpected username: %s", body.Username)
			}
			_, _ = w.Write([]byte(`{"result":1,"data":{"token":"AUTH_TOKEN"}}`))
		case "getLiveStreamingLink":
			gotAuthCode = r.Header.Get("auth-code")
			var body struct {
				PlateNo   string `json:"plate_no"`
				ChannelID int    `json:"channel_id"`
				ProjectID int    `json:"project_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.PlateNo != "GJ03BZ0211" || body.ChannelID != 4 || body.ProjectID != 37 {
				t.Fatalf("unexpected body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"result":"success","jspLink":"https://trakzee2.uffizio.com/jsp/liveStream.jsp?param=XXXXX","message":"Streaming started successfully."}`))
		default:
			t.Fatalf("unexpected token param: %s", r.URL.Query().Get("token"))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())

	token, err := c.GetAccessToken("nishant.dhaktod@sumith.in", "Login@123")
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if token != "AUTH_TOKEN" {
		t.Fatalf("unexpected token: %q", token)
	}

	link, err := c.GetLiveStreamingLink(token, "GJ03BZ0211", 4, 37)
	if err != nil {
		t.Fatalf("GetLiveStreamingLink: %v", err)
	}
	if link != "https://trakzee2.uffizio.com/jsp/liveStream.jsp?param=XXXXX" {
		t.Fatalf("unexpected link: %q", link)
	}
	if gotAuthCode != "AUTH_TOKEN" {
		t.Fatalf("expected auth-code header to carry the token, got %q", gotAuthCode)
	}
}

func TestListVehicles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "getUserWiseLiveData" {
			t.Fatalf("unexpected token param: %s", r.URL.Query().Get("token"))
		}
		if r.URL.Query().Get("user") != "u" || r.URL.Query().Get("pass") != "p" {
			t.Fatalf("unexpected user/pass: %s/%s", r.URL.Query().Get("user"), r.URL.Query().Get("pass"))
		}
		_, _ = w.Write([]byte(`[{"plate_no":"TS12SU3339"},{"plate_no":"KA32F2321"}]`))
	}))
	defer srv.Close()

	vehicles, err := NewClient(srv.URL, srv.Client()).ListVehicles("u", "p")
	if err != nil {
		t.Fatalf("ListVehicles: %v", err)
	}
	if len(vehicles) != 2 || vehicles[0].PlateNo != "TS12SU3339" {
		t.Fatalf("unexpected vehicles: %+v", vehicles)
	}
}
