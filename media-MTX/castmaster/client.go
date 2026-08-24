// Package castmaster is a Go client for the Castmaster Mobility CCTV HTTP API
// ("Interfacing 'CM CCTV systems'... API Document"). It covers the
// video-streaming surface: login, live FLV preview URLs, history (HIS/HLS)
// playback URLs, and the async video-download task lifecycle.
package castmaster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single Castmaster server instance.
type Client struct {
	BaseURL    string // e.g. "https://cmmipl.org:22056"
	HTTPClient *http.Client

	key string
}

// NewClient builds a Client. If httpClient is nil, a client with a 15s
// timeout is used.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: httpClient}
}

// Key returns the verify key obtained from Login, if any.
func (c *Client) Key() string { return c.key }

// SetKey lets the caller install a previously-obtained key (e.g. cached
// across process restarts) without calling Login again.
func (c *Client) SetKey(key string) { c.key = key }

// APIError is returned when the server responds with a non-success
// errorcode.
type APIError struct {
	Code  int
	Cause string
}

func (e *APIError) Error() string {
	if e.Cause != "" {
		return fmt.Sprintf("castmaster: errorcode=%d: %s", e.Code, e.Cause)
	}
	return fmt.Sprintf("castmaster: errorcode=%d", e.Code)
}

// envelope is the common {data, errorcode, errorcase} response shape. A few
// endpoints (evidence list/alarm-track) add sibling fields alongside "data"
// (e.g. "context", "result" instead of "data"); those are decoded separately
// via doRaw so callers can reach the extra fields.
type envelope struct {
	Data       json.RawMessage `json:"data"`
	Result     json.RawMessage `json:"result"` // used by relatedgpsalarm instead of "data"
	Context    string          `json:"context"`
	ErrorCode  int             `json:"errorcode"`
	ErrorCause string          `json:"errorcase"` // some endpoints use "errorcase" instead of a data field
}

// doRaw performs the HTTP round trip and returns the decoded envelope
// without unmarshaling env.Data into a caller type, so endpoints with
// nonstandard response shapes (extra sibling fields) can inspect it
// directly.
func (c *Client) doRaw(req *http.Request) (envelope, error) {
	if c.key != "" {
		req.Header.Set("key", c.key)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return envelope{}, fmt.Errorf("castmaster: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope{}, fmt.Errorf("castmaster: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return envelope{}, fmt.Errorf("castmaster: http %d: %s", resp.StatusCode, string(body))
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, fmt.Errorf("castmaster: decode response: %w (body=%s)", err, string(body))
	}
	if env.ErrorCode != 200 && env.ErrorCode != 0 {
		return env, &APIError{Code: env.ErrorCode, Cause: env.ErrorCause}
	}
	return env, nil
}

func (c *Client) do(req *http.Request, out any) error {
	env, err := c.doRaw(req)
	if err != nil {
		return err
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("castmaster: decode data: %w", err)
		}
	}
	return nil
}

func (c *Client) get(path string, query url.Values, out any) error {
	u := c.BaseURL + path
	if query != nil {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) postJSON(method, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("castmaster: encode request: %w", err)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// Login exchanges a username/password for a verify key and stores it on the
// client for subsequent calls. GET /api/v1/basic/key
func (c *Client) Login(username, password string) (string, error) {
	q := url.Values{"username": {username}, "password": {password}}
	var out struct {
		Key string `json:"key"`
	}
	// This endpoint returns the key at the top level of "data", no key header
	// is required (or available) yet, so bypass c.get's header injection.
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/v1/basic/key?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	c.key = out.Key
	return c.key, nil
}

// VideoPort is one available live-preview relay port.
type VideoPort struct {
	Port int `json:"port"`
}

// LivePorts lists the video pass-through ports configured on the server.
// GET /api/v1/basic/live/port
func (c *Client) LivePorts() ([]VideoPort, error) {
	var out []VideoPort
	err := c.get("/api/v1/basic/live/port", nil, &out)
	return out, err
}

// StreamType selects the codestream for history/download video requests
// (§5.1-5.3, §6.1 of the doc): 0 = sub stream, 1 = main stream.
//
// The live-video endpoint (§4.2) uses the OPPOSITE convention for its
// same-named "st" parameter (0 = main, 1 = sub) — that's LiveStreamType,
// deliberately a distinct type so the two can't be swapped by mistake.
type StreamType int

const (
	StreamMain StreamType = 1
	StreamSub  StreamType = 0
)

// LiveStreamType selects the codestream for the live-video endpoint only
// (§4.2: "st:[int] Codestream type 0: main codestream, 1: sub codestream").
// Do not reuse StreamType/StreamMain/StreamSub here — the doc defines the
// opposite 0/1 meaning for history/download requests (see StreamType).
type LiveStreamType int

const (
	LiveStreamMain LiveStreamType = 0
	LiveStreamSub  LiveStreamType = 1
)

// DeviceKind disambiguates the dt= parameter for LiveVideoURL: n9m devices
// omit it, CMS/MDVR devices must send "MDVR".
type DeviceKind string

const (
	DeviceN9M  DeviceKind = ""
	DeviceMDVR DeviceKind = "MDVR"
)

// LiveVideoURL requests the playable FLV URL for one channel of one device
// on the given relay port (obtained from LivePorts).
// GET /api/v1/basic/live/video
func (c *Client) LiveVideoURL(terid string, channel int, audio bool, st LiveStreamType, port int, dt DeviceKind) (string, error) {
	q := url.Values{
		"terid": {terid},
		"chl":   {strconv.Itoa(channel)},
		"audio": {boolToFlag(audio)},
		"st":    {strconv.Itoa(int(st))},
		"port":  {strconv.Itoa(port)},
	}
	if dt != "" {
		q.Set("dt", string(dt))
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := c.get("/api/v1/basic/live/video", q, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// CalendarDay is one entry in the history-video monthly calendar.
type CalendarDay struct {
	Date     string `json:"date"`     // yyyy-MM-dd
	FileType int    `json:"filetype"` // 1: normal recording, 2: alarm recording
}

// HistoryCalendar lists the dates in startTime's month that have recordings.
// startTime format: "2006-01-02". GET /api/v1/basic/record/calendar
func (c *Client) HistoryCalendar(terid, startTime string, st StreamType, pbt int) ([]CalendarDay, error) {
	q := url.Values{
		"terid":     {terid},
		"starttime": {startTime},
		"st":        {strconv.Itoa(int(st))},
		"pbt":       {strconv.Itoa(pbt)},
	}
	var out []CalendarDay
	err := c.get("/api/v1/basic/record/calendar", q, &out)
	return out, err
}

// HistoryFile is one recorded segment.
type HistoryFile struct {
	Name      string `json:"name"`
	Channel   int    `json:"chn"`
	FileType  int    `json:"filetype"`
	StartTime string `json:"starttime"`
	EndTime   string `json:"endtime"`
}

// HistoryFileList lists recorded segments for a device over a time range.
// startTime/endTime format: "2006-01-02 15:04:05".
// GET /api/v1/basic/record/filelist
func (c *Client) HistoryFileList(terid, startTime, endTime string, channels []int, st StreamType, pbt int) ([]HistoryFile, error) {
	chlStrs := make([]string, len(channels))
	for i, ch := range channels {
		chlStrs[i] = strconv.Itoa(ch)
	}
	q := url.Values{
		"terid":     {terid},
		"starttime": {startTime},
		"endtime":   {endTime},
		"chl":       {strings.Join(chlStrs, ",")},
		"st":        {strconv.Itoa(int(st))},
		"pbt":       {strconv.Itoa(pbt)},
	}
	var out []HistoryFile
	err := c.get("/api/v1/basic/record/filelist", q, &out)
	return out, err
}

// HistoryStreamURL requests the m3u8 (HLS) playback URL for a history
// segment window on one channel. GET /api/v1/basic/record/video
func (c *Client) HistoryStreamURL(terid, startTime, endTime string, channel int, st StreamType) (string, error) {
	q := url.Values{
		"terid":     {terid},
		"starttime": {startTime},
		"endtime":   {endTime},
		"chl":       {strconv.Itoa(channel)},
		"st":        {strconv.Itoa(int(st))},
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := c.get("/api/v1/basic/record/video", q, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// NetMode restricts which network the device may use when running a
// download task.
type NetMode int

const (
	NetModeLAN     NetMode = 1
	NetModeWiFi    NetMode = 2
	NetModeWiFiLAN NetMode = 3
	NetMode4G      NetMode = 4
	NetModeAll     NetMode = 7
)

// TaskType selects what a download task pulls: black box, video recording,
// or both.
type TaskType int

const (
	TaskTypeBlackBox       TaskType = 0
	TaskTypeVideoRecording TaskType = 1
	TaskTypeAll            TaskType = 2
)

// CreateDownloadTaskParams describes a video-download job to run on-device.
type CreateDownloadTaskParams struct {
	Terid     string
	StartTime string // "2006-01-02 15:04:05"
	EndTime   string
	Channels  []int
	Name      string // max 15 characters
	Effective int    // effective days
	NetMode   NetMode
	TaskType  TaskType
	Stream    StreamType
}

// CreateDownloadTask starts an on-device video download task and returns its
// task ID. POST /api/v1/basic/record/task
func (c *Client) CreateDownloadTask(p CreateDownloadTaskParams) (int, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     p.Terid,
		"starttime": p.StartTime,
		"endtime":   p.EndTime,
		"chl":       p.Channels,
		"name":      p.Name,
		"effective": p.Effective,
		"netmode":   int(p.NetMode),
		"tasktype":  int(p.TaskType),
		"stream":    int(p.Stream),
	}
	var out struct {
		TaskID int `json:"taskid"`
	}
	if err := c.postJSON(http.MethodPost, "/api/v1/basic/record/task", body, &out); err != nil {
		return 0, err
	}
	return out.TaskID, nil
}

// DownloadTaskState describes the progress of one queued download task.
type DownloadTaskState struct {
	Percent int `json:"percent"`
	State   int `json:"state"` // see doc: -6..8, 3 = completed
	TaskID  int `json:"taskid"`
}

// TaskCompleted reports whether the state indicates a finished (successful)
// task.
func (s DownloadTaskState) TaskCompleted() bool { return s.State == 3 }

// DownloadTaskStatus polls the status of one or more (taskID, date) pairs.
// POST /api/v1/basic/record/taskstate
func (c *Client) DownloadTaskStatus(taskID int, date string) ([]DownloadTaskState, error) {
	body := map[string]any{
		"key": c.key,
		"parms": []map[string]any{
			{"taskid": taskID, "date": date},
		},
	}
	var out []DownloadTaskState
	err := c.postJSON(http.MethodPost, "/api/v1/basic/record/taskstate", body, &out)
	return out, err
}

// TaskFile is one completed-task video file, ready to download.
type TaskFile struct {
	Dir  string `json:"dir"` // base64-encoded directory
	Name string `json:"name"`
}

// DownloadTaskFileList lists the files produced by a completed download
// task. taskType: 0=black box, 1=video (default), 2=all.
// GET /api/v1/basic/record/taskfilelist
func (c *Client) DownloadTaskFileList(taskID int, taskType TaskType) ([]TaskFile, error) {
	q := url.Values{
		"taskid":   {strconv.Itoa(taskID)},
		"tasktype": {strconv.Itoa(int(taskType))},
	}
	var out []TaskFile
	err := c.get("/api/v1/basic/record/taskfilelist", q, &out)
	return out, err
}

// DownloadFile streams the raw bytes of a completed task's recording file.
// The caller must close the returned ReadCloser.
// GET /api/v1/basic/record/download
func (c *Client) DownloadFile(dir, name string) (io.ReadCloser, error) {
	q := url.Values{"key": {c.key}, "dir": {dir}, "name": {name}}
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/v1/basic/record/download?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("castmaster: download request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("castmaster: download http %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// DeleteDownloadTask removes a previously created download task.
// DELETE /api/v1/basic/record/task
func (c *Client) DeleteDownloadTask(taskID int) error {
	body := map[string]any{
		"key":   c.key,
		"parms": []map[string]any{{"taskid": taskID}},
	}
	var out struct {
		Result bool `json:"result"`
	}
	if err := c.postJSON(http.MethodDelete, "/api/v1/basic/record/task", body, &out); err != nil {
		return err
	}
	if !out.Result {
		return fmt.Errorf("castmaster: delete task %d: server reported failure", taskID)
	}
	return nil
}

func boolToFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
