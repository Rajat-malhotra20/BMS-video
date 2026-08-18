package castmaster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAndLiveVideoURL(t *testing.T) {
	var gotKeyHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/basic/key":
			if r.URL.Query().Get("username") != "user1" {
				t.Fatalf("unexpected username: %s", r.URL.Query().Get("username"))
			}
			writeEnvelope(w, `{"key":"abc123"}`, 200, "")
		case "/api/v1/basic/live/port":
			gotKeyHeader = r.Header.Get("key")
			writeEnvelope(w, `[{"port":12060},{"port":12061}]`, 200, "")
		case "/api/v1/basic/live/video":
			if r.URL.Query().Get("terid") != "00830007CB" {
				t.Fatalf("unexpected terid: %s", r.URL.Query().Get("terid"))
			}
			if r.URL.Query().Get("chl") != "1" {
				t.Fatalf("unexpected chl: %s", r.URL.Query().Get("chl"))
			}
			// Per the doc's §4.2 live-video table, st=0 means main codestream
			// (the OPPOSITE of the history/download "st" convention) — this
			// locks in the fix for a prior inverted-constant bug.
			if r.URL.Query().Get("st") != "0" {
				t.Fatalf("expected st=0 for LiveStreamMain, got %q", r.URL.Query().Get("st"))
			}
			writeEnvelope(w, `{"url":"https://cmmipl.org:12060/live.flv?devid=X&chl=1"}`, 200, "")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())

	key, err := c.Login("user1", "pass1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if key != "abc123" || c.Key() != "abc123" {
		t.Fatalf("expected key abc123, got %q", key)
	}

	ports, err := c.LivePorts()
	if err != nil {
		t.Fatalf("LivePorts: %v", err)
	}
	if len(ports) != 2 || ports[0].Port != 12060 {
		t.Fatalf("unexpected ports: %+v", ports)
	}
	if gotKeyHeader != "abc123" {
		t.Fatalf("expected key header to be set on authenticated call, got %q", gotKeyHeader)
	}

	url, err := c.LiveVideoURL("00830007CB", 1, true, LiveStreamMain, 12060, DeviceN9M)
	if err != nil {
		t.Fatalf("LiveVideoURL: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty stream url")
	}
}

func TestAPIErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, `null`, 401, "invalid key")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("bad-key")

	_, err := c.LivePorts()
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != 401 {
		t.Fatalf("expected code 401, got %d", apiErr.Code)
	}
}

func TestCreateAndPollDownloadTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/basic/record/task":
			if r.Method == http.MethodPost {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["name"] != "test" {
					t.Fatalf("unexpected body: %+v", body)
				}
				writeEnvelope(w, `{"taskid":1}`, 200, "")
				return
			}
			t.Fatalf("unexpected method %s", r.Method)
		case "/api/v1/basic/record/taskstate":
			writeEnvelope(w, `[{"percent":100,"state":3,"taskid":1}]`, 200, "")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	c.SetKey("k")

	taskID, err := c.CreateDownloadTask(CreateDownloadTaskParams{
		Terid:     "00830007CB",
		StartTime: "2026-04-01 00:00:00",
		EndTime:   "2026-04-01 08:00:00",
		Channels:  []int{1, 2, 3, 4},
		Name:      "test",
		Effective: 7,
		NetMode:   NetModeAll,
		TaskType:  TaskTypeVideoRecording,
		Stream:    StreamMain,
	})
	if err != nil {
		t.Fatalf("CreateDownloadTask: %v", err)
	}
	if taskID != 1 {
		t.Fatalf("expected taskid 1, got %d", taskID)
	}

	states, err := c.DownloadTaskStatus(taskID, "2026-04-01")
	if err != nil {
		t.Fatalf("DownloadTaskStatus: %v", err)
	}
	if len(states) != 1 || !states[0].TaskCompleted() {
		t.Fatalf("expected completed task, got %+v", states)
	}
}

func writeEnvelope(w http.ResponseWriter, data string, code int, cause string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":` + data + `,"errorcode":` + jsonInt(code) + `,"errorcase":"` + cause + `"}`))
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
