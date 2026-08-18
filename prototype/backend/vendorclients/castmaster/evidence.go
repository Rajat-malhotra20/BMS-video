package castmaster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// EvidenceCount queries the total number of vehicle evidence records
// matching a filter. types/keyType/keyword/timeType follow the same rules
// as EvidenceList. POST /api/v1/basic/evidence-center/count
func (c *Client) EvidenceCount(terids []string, types []int, startTime, endTime string, keyType int, keyword string, timeType int) (int, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     terids,
		"starttime": startTime,
		"endtime":   endTime,
		"type":      types,
		"keytype":   keyType,
		"keyword":   keyword,
		"timetype":  timeType,
	}
	var out struct {
		Total int `json:"total"`
	}
	err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/count", body, &out)
	return out.Total, err
}

// EvidenceRecord is one entry from the evidence search list.
type EvidenceRecord struct {
	Address            string  `json:"address"`
	AlarmID            string  `json:"alarmid"`
	AlarmLevel         int     `json:"alarmlevel"`
	AlarmType          int     `json:"alarmtype"`
	CreateTime         string  `json:"createtime"`
	DriverLicense      string  `json:"driverlicense"`
	DriverName         string  `json:"drivername"`
	DriverPhone        string  `json:"driverphone"`
	DriverPhoto        string  `json:"driverphoto"`
	EID                string  `json:"eid"`
	EName              string  `json:"ename"`
	EndTime            string  `json:"endtime"`
	GPSLat             float64 `json:"gpslat"`
	GPSLng             float64 `json:"gpslng"`
	Pic                string  `json:"pic"` // base64-encoded cover image path
	Sec                int     `json:"sec"`
	Size               float64 `json:"size"` // MB
	StartTime          string  `json:"starttime"`
	Terid              string  `json:"terid"`
	Time               string  `json:"time"`
	Vehicle            string  `json:"vehicle"`
	EvidenceStatus     int     `json:"evidencestatus"`
	EvidenceStatusMsg  string  `json:"evidencestatusmsg"`
	EvidenceServerName string  `json:"evidenceservername"`
	Direction          int     `json:"direction"`
	Desc               string  `json:"desc"`
}

// EvidenceListParams is the request body for EvidenceList.
type EvidenceListParams struct {
	Terid     []string
	StartTime string
	EndTime   string
	Type      []int // alarm types, empty = all
	KeyType   int   // -1: none, 0: driver, 1: license plate, 2: evidence name
	Keyword   string
	Page      int    // -1: no paging, return all
	Count     int    // page size
	Context   string // context returned from the previous call; "" on first call
	TimeType  int    // 0: alarm time, 1: evidence generation time
}

// EvidenceListResult is EvidenceList's return value: the matched records
// plus a Context token to pass into the next call (the server may return
// different results per call, and pagination depends on replaying Context
// verbatim per the doc's special note).
type EvidenceListResult struct {
	Records []EvidenceRecord
	Context string
}

// EvidenceList queries the vehicle evidence list under the authenticated
// account. POST /api/v1/basic/evidence-center/list
func (c *Client) EvidenceList(p EvidenceListParams) (EvidenceListResult, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     p.Terid,
		"starttime": p.StartTime,
		"endtime":   p.EndTime,
		"type":      p.Type,
		"keytype":   p.KeyType,
		"keyword":   p.Keyword,
		"page":      p.Page,
		"count":     p.Count,
		"context":   p.Context,
		"timetype":  p.TimeType,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return EvidenceListResult{}, fmt.Errorf("castmaster: encode request: %w", err)
	}
	req, err := newJSONRequest(http.MethodPost, c.BaseURL+"/api/v1/basic/evidence-center/list", buf)
	if err != nil {
		return EvidenceListResult{}, err
	}
	env, err := c.doRaw(req)
	if err != nil {
		return EvidenceListResult{}, err
	}
	var records []EvidenceRecord
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &records); err != nil {
			return EvidenceListResult{}, fmt.Errorf("castmaster: decode data: %w", err)
		}
	}
	return EvidenceListResult{Records: records, Context: env.Context}, nil
}

// EvidencePicture is one channel's cover image for a piece of evidence.
type EvidencePicture struct {
	Channel int    `json:"channel"`
	Path    string `json:"path"` // base64-encoded image path
}

// EvidencePictureList lists the cover images for one evidence ID.
// POST /api/v1/basic/evidence-center/picture/list
func (c *Client) EvidencePictureList(eid string) ([]EvidencePicture, error) {
	body := map[string]any{"key": c.key, "eid": eid}
	var out []EvidencePicture
	err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/picture/list", body, &out)
	return out, err
}

// EvidenceDetail is the full metadata for one piece of evidence.
type EvidenceDetail struct {
	EID               string  `json:"eid"`
	Size              float64 `json:"size"` // MB
	Terid             string  `json:"terid"`
	GroupName         string  `json:"groupname"`
	CarLicense        string  `json:"carlicense"`
	PlateColor        int     `json:"platecolor"`
	AlarmType         int     `json:"alarmtype"`
	Speed             float64 `json:"speed"`
	StartTime         string  `json:"starttime"`
	EndTime           string  `json:"endtime"`
	Lat               float64 `json:"lat"`
	Lng               float64 `json:"lng"`
	Location          string  `json:"location"`
	DriverName        string  `json:"drivername"`
	DriverPhone       string  `json:"driverphone"`
	DriverLicense     string  `json:"driverlicense"`
	DriverImg         string  `json:"driverimg"`
	HandleUsername    string  `json:"handleusername"`
	HandleTime        string  `json:"handletime"`
	HandleMethod      int     `json:"handlemethod"`
	HandleContent     string  `json:"handlecontent"`
	EvidenceStatus    int     `json:"evidencestatus"`
	EvidenceStatusMsg string  `json:"evidencestatusmsg"`
}

// EvidenceDetailList fetches full metadata for one or more evidence IDs.
// POST /api/v1/basic/evidence-center/detail
func (c *Client) EvidenceDetailList(eids []string) ([]EvidenceDetail, error) {
	body := map[string]any{"key": c.key, "eid": eids}
	var out []EvidenceDetail
	err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/detail", body, &out)
	return out, err
}

// EvidenceVideoSegment is one clip within an evidence video's channel.
type EvidenceVideoSegment struct {
	StartTime string `json:"starttime"`
	EndTime   string `json:"endtime"`
	Path      string `json:"path"` // base64 + url encoded
}

// EvidenceVideoChannel groups an evidence video's segments by channel.
type EvidenceVideoChannel struct {
	Channel int                    `json:"channel"`
	Video   []EvidenceVideoSegment `json:"video"`
}

// EvidenceVideoType selects the container for EvidenceVideoList.
type EvidenceVideoType int

const (
	EvidenceVideo264 EvidenceVideoType = 0
	EvidenceVideoMP4 EvidenceVideoType = 1
)

// EvidenceVideoList lists the video files for one piece of evidence.
// POST /api/v1/basic/evidence-center/video/list
func (c *Client) EvidenceVideoList(eid string, videoType EvidenceVideoType) ([]EvidenceVideoChannel, error) {
	body := map[string]any{"key": c.key, "eid": eid, "type": int(videoType)}
	var out []EvidenceVideoChannel
	err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/video/list", body, &out)
	return out, err
}

// GenerateEvidenceFile packs one piece of evidence into a zip archive on
// serverIP and returns its (base64+url encoded) download path.
// POST /api/v1/basic/evidence-center/filepack
func (c *Client) GenerateEvidenceFile(serverIP, eid string) (string, error) {
	body := map[string]any{"key": c.key, "serverip": serverIP, "eid": eid}
	var out struct {
		Path string `json:"path"`
	}
	if err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/filepack", body, &out); err != nil {
		return "", err
	}
	return out.Path, nil
}

// EvidenceServerInfo is one evidence-storage server's connection details.
type EvidenceServerInfo struct {
	ServerIP       string `json:"serverip"`
	ServerName     string `json:"servername"` // matches EvidenceRecord.EvidenceServerName
	ServerRectPort int    `json:"serverrectport"`
	WCMS5Port      int    `json:"wcms5port"`
}

// EvidenceServerInfoList queries the system's evidence-distribution server
// pool. POST /api/v1/basic/evidence-center/evidenceserverinfo
func (c *Client) EvidenceServerInfoList() ([]EvidenceServerInfo, error) {
	body := map[string]any{"key": c.key}
	var out []EvidenceServerInfo
	err := c.postJSON(http.MethodPost, "/api/v1/basic/evidence-center/evidenceserverinfo", body, &out)
	return out, err
}

// RelatedAlarm is one alarm/GPS point associated with a piece of evidence.
type RelatedAlarm struct {
	AlarmType int     `json:"alarmType"`
	IsCurrent int     `json:"isCurrent"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Loc       string  `json:"loc"`
	Time      string  `json:"time"`
	UUID      string  `json:"uuid"`
}

// EvidenceAlarmTrack is the alarm/GPS track associated with one piece of
// evidence.
type EvidenceAlarmTrack struct {
	RelatedGPSLong  string         `json:"relatedGpsLong"`  // ';'-separated GPS points, +/-1hr window
	RelatedGPSShort string         `json:"relatedGpsShort"` // ';'-separated GPS points, +/-5min window
	RelatedAlarm    []RelatedAlarm `json:"relatedAlarm"`
}

// EvidenceAlarmTrack queries the alarm and GPS trajectory associated with
// one piece of evidence. Note this endpoint's response uses a top-level
// "result" field (and "errorcase" for the message) rather than "data".
// POST /api/v1/basic/evidence-center/relatedgpsalarm
func (c *Client) EvidenceAlarmTrack(eid string) (EvidenceAlarmTrack, error) {
	body := map[string]any{"key": c.key, "eid": eid}
	buf, err := json.Marshal(body)
	if err != nil {
		return EvidenceAlarmTrack{}, fmt.Errorf("castmaster: encode request: %w", err)
	}
	req, err := newJSONRequest(http.MethodPost, c.BaseURL+"/api/v1/basic/evidence-center/relatedgpsalarm", buf)
	if err != nil {
		return EvidenceAlarmTrack{}, err
	}
	env, err := c.doRaw(req)
	if err != nil {
		return EvidenceAlarmTrack{}, err
	}
	var out EvidenceAlarmTrack
	if len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, &out); err != nil {
			return EvidenceAlarmTrack{}, fmt.Errorf("castmaster: decode result: %w", err)
		}
	}
	return out, nil
}

// TalkToken is the websocket connection info for the two-way intercom
// (VOIP) feature.
type TalkToken struct {
	URL     string `json:"url"`
	SvrIP   string `json:"svrip"`
	SvrPort int    `json:"svrport"`
}

// TalkToken requests the websocket token used to start an intercom session
// with the given device (see the doc's "Two Way VOIP Calling" section for
// the client-side html265.js usage that follows).
// POST /api/v1/basic/talk/token (terid and key are sent as query params,
// per the doc's example, not as a JSON body).
func (c *Client) TalkToken(terid string) (TalkToken, error) {
	q := url.Values{"terid": {terid}, "key": {c.key}}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/v1/basic/talk/token?"+q.Encode(), nil)
	if err != nil {
		return TalkToken{}, err
	}
	var out TalkToken
	if err := c.do(req, &out); err != nil {
		return TalkToken{}, err
	}
	return out, nil
}

func newJSONRequest(method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
