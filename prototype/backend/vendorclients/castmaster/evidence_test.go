package castmaster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvidenceListReturnsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/basic/evidence-center/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"eid":"abc","ename":"EMR"}],"context":"next-page-token","errorcode":200}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("k")

	result, err := c.EvidenceList(EvidenceListParams{Terid: []string{"008A000152"}, Page: 1, Count: 10})
	if err != nil {
		t.Fatalf("EvidenceList: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].EID != "abc" {
		t.Fatalf("unexpected records: %+v", result.Records)
	}
	if result.Context != "next-page-token" {
		t.Fatalf("expected context token, got %q", result.Context)
	}
}

func TestEvidenceAlarmTrackUsesResultField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"errorcode":200,
			"errorcase":"Success!",
			"result":{
				"relatedGpsLong":"",
				"relatedGpsShort":"",
				"relatedAlarm":[{"alarmType":1,"isCurrent":1,"lat":23.59,"lng":104.11,"loc":"","time":"2026-04-19 11:03:05","uuid":"0099002BB1_6_606"}]
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("k")

	track, err := c.EvidenceAlarmTrack("hdtgMmLzRLDp1HQMbfRXvtc%3D")
	if err != nil {
		t.Fatalf("EvidenceAlarmTrack: %v", err)
	}
	if len(track.RelatedAlarm) != 1 || track.RelatedAlarm[0].UUID != "0099002BB1_6_606" {
		t.Fatalf("unexpected track: %+v", track)
	}
}

func TestTalkTokenSendsQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Query().Get("terid") != "T1" {
			t.Fatalf("expected terid=T1, got %q", r.URL.Query().Get("terid"))
		}
		writeEnvelope(w, `{"url":"wss://example.com/talk","svrip":"127.0.0.1","svrport":5550}`, 200, "")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("k")

	token, err := c.TalkToken("T1")
	if err != nil {
		t.Fatalf("TalkToken: %v", err)
	}
	if token.URL == "" || token.SvrPort != 5550 {
		t.Fatalf("unexpected token: %+v", token)
	}
}

func TestAlarmCountAndPassengerFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/basic/alarm/count":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["type"].(float64) != 13 {
				t.Fatalf("expected type 13, got %v", body["type"])
			}
			writeEnvelope(w, `[{"terid":"AE99873120","date":"2026-04-19","count":5687}]`, 200, "")
		case "/api/v1/basic/passenger-count/detail":
			writeEnvelope(w, `[{"closetime":"2026-04-11 14:39:22","door":"","off":2,"on":3,"opentime":"2026-04-11 14:39:22","sitename":"AAAAA","terid":"661","time":"2026-04-11 14:39:22","lat":25.331,"lng":115.5598}]`, 200, "")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("k")

	counts, err := c.AlarmCount([]string{"AE99873120"}, 13, "2026-04-19 00:00:00", "2026-04-19 23:59:59")
	if err != nil {
		t.Fatalf("AlarmCount: %v", err)
	}
	if len(counts) != 1 || counts[0].Count != 5687 {
		t.Fatalf("unexpected counts: %+v", counts)
	}

	flow, err := c.PassengerFlowDetail([]string{"661"}, "2026-04-07 00:00:00", "2026-04-13 23:59:59", "")
	if err != nil {
		t.Fatalf("PassengerFlowDetail: %v", err)
	}
	if len(flow) != 1 || flow[0].On != 3 {
		t.Fatalf("unexpected flow: %+v", flow)
	}
}
