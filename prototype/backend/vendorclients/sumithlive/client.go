// Package sumithlive implements the Sumith/Trakzee "Live Camera Streaming"
// HTTP API (trakzee2.uffizio.com) — unrelated to the sumith package's
// CCU-SCU telemetry protocol. The documented call (getLiveStreamingLink)
// only returns a jspLink page URL, but that page itself just renders a
// fixed 4-camera grid by fetching direct HLS URLs — confirmed live via
// browser devtools (2026-08-18): the jspLink's base64 "param" decodes to
// a tilde-delimited string whose first field is the device's IMEI, and
// {imei}_cam{1..4}.m3u8 under a separate HLS host are real, directly
// playable, CORS-open playlists — independent per camera (some channels
// 404 if that input has no live feed right now, others serve real
// segments). None of this is in the vendor's published PDF; see
// DecodeDeviceID/HLSURL/ProbeLive below.
package sumithlive

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://trakzee2.uffizio.com"
	// defaultHLSBase is the host serving direct per-camera HLS playlists,
	// reverse-engineered from one real account's network trace — NOT
	// documented anywhere, and not necessarily the same for every Sumith/
	// Trakzee tenant. Override via NewClient's hlsBase param if a
	// different account turns out to use a different host.
	defaultHLSBase = "https://rtmpvideo.uffizio.com/hls"
)

// Client talks to one Sumith/Trakzee account's webservice endpoint.
type Client struct {
	baseURL string
	hlsBase string
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, hlsBase: defaultHLSBase, http: httpClient}
}

// SetHLSBase overrides the reverse-engineered HLS host (see defaultHLSBase)
// for accounts where it turns out to differ.
func (c *Client) SetHLSBase(hlsBase string) { c.hlsBase = hlsBase }

// GetAccessToken exchanges username/password for an auth token
// (POST /webservice?token=generateAccessToken).
func (c *Client) GetAccessToken(username, password string) (string, error) {
	var out struct {
		Result int `json:"result"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := c.post("generateAccessToken", "", map[string]string{
		"username": username, "password": password,
	}, &out); err != nil {
		return "", err
	}
	if out.Result != 1 || out.Data.Token == "" {
		return "", fmt.Errorf("sumithlive: generateAccessToken did not return a token (result=%d)", out.Result)
	}
	return out.Data.Token, nil
}

// GetLiveStreamingLink resolves a vehicle's live-video page URL
// (POST /webservice?token=getLiveStreamingLink). plateNo is the vehicle
// registration plate (this API has no device-serial concept); channelID
// selects the camera on that vehicle; projectID scopes the Trakzee tenant.
func (c *Client) GetLiveStreamingLink(authToken, plateNo string, channelID, projectID int) (string, error) {
	var out struct {
		Result  string `json:"result"`
		JSPLink string `json:"jspLink"`
		Message string `json:"message"`
	}
	if err := c.post("getLiveStreamingLink", authToken, map[string]any{
		"plate_no": plateNo, "channel_id": channelID, "project_id": projectID,
	}, &out); err != nil {
		return "", err
	}
	if out.Result != "success" || out.JSPLink == "" {
		return "", fmt.Errorf("sumithlive: getLiveStreamingLink failed: %s", out.Message)
	}
	return out.JSPLink, nil
}

// DecodeDeviceID extracts the device IMEI from a jspLink returned by
// GetLiveStreamingLink. The link's "param" query value is base64 of a
// tilde-delimited string, e.g. "864819050951795~210807~01~2517~~
// streaming1.uffizio.com~yes~12457" — field 0 is the IMEI used in HLS
// URLs (HLSURL). Reverse-engineered; not in the vendor's doc.
func DecodeDeviceID(jspLink string) (string, error) {
	u, err := url.Parse(jspLink)
	if err != nil {
		return "", fmt.Errorf("sumithlive: parse jspLink: %w", err)
	}
	param := u.Query().Get("param")
	if param == "" {
		return "", fmt.Errorf("sumithlive: jspLink has no param query value")
	}
	raw, err := base64.StdEncoding.DecodeString(param)
	if err != nil {
		return "", fmt.Errorf("sumithlive: decode jspLink param: %w", err)
	}
	fields := strings.Split(string(raw), "~")
	if len(fields) == 0 || fields[0] == "" {
		return "", fmt.Errorf("sumithlive: jspLink param has no device id field")
	}
	return fields[0], nil
}

// HLSURL builds the direct per-camera HLS playlist URL for one channel
// (1-4 observed) of the given device. Call ProbeLive before handing this
// to a player — some channels 404 when that camera input has no live
// feed right now.
func (c *Client) HLSURL(deviceID string, channel int) string {
	return fmt.Sprintf("%s/%s_cam%d.m3u8", c.hlsBase, deviceID, channel)
}

// probeTimeout bounds ProbeLive regardless of the Client's own http.Client
// timeout (which may be http.DefaultClient, i.e. none) — a hung probe on
// an offline camera must not hang the request that's checking it.
const probeTimeout = 5 * time.Second

// ProbeLive reports whether an HLSURL is genuinely playable right now —
// not just that the .m3u8 itself responds. Confirmed live (2026-08-18) that
// a dead camera's playlist can keep returning 200/304 indefinitely (nginx
// serving a stale cached manifest) while every segment it references is
// 404 — a HEAD on the manifest alone reports "live" for a camera that's
// actually a black box. So this fetches the playlist body, finds the last
// referenced segment, and confirms THAT is fetchable too.
func (c *Client) ProbeLive(hlsURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hlsURL, nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return false
	}

	lastSegment := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lastSegment = line
		}
	}
	if lastSegment == "" {
		return false // empty playlist — nothing to actually play
	}

	segURL := lastSegment
	if !strings.HasPrefix(segURL, "http://") && !strings.HasPrefix(segURL, "https://") {
		segURL = c.hlsBase + "/" + lastSegment
	}

	segReq, err := http.NewRequestWithContext(ctx, http.MethodHead, segURL, nil)
	if err != nil {
		return false
	}
	segResp, err := c.http.Do(segReq)
	if err != nil {
		return false
	}
	defer segResp.Body.Close()
	return segResp.StatusCode == http.StatusOK
}

// Vehicle is one entry from ListVehicles. Latitude/Longitude/GPSDateTime
// are the closest thing this account-wide call gives to a liveness signal
// — there's no per-camera online status in this API.
type Vehicle struct {
	PlateNo     string `json:"plate_no"`
	Latitude    string `json:"latitude"`
	Longitude   string `json:"longitude"`
	GPSDateTime string `json:"gps_date_time"` // "02-01-2006 15:04:05", account-local time
}

// ListVehicles returns the plates visible to this account, via the wider
// Uffizio Tracking API's getUserWiseLiveData (not documented in the
// Sumith-branded live-streaming PDF, which has no plate-discovery endpoint
// at all — this is the same trakzee2.uffizio.com platform, confirmed
// working against the real account). Auth here is plain user/pass query
// params, unlike the token-header flow used by GetLiveStreamingLink.
func (c *Client) ListVehicles(username, password string) ([]Vehicle, error) {
	q := url.Values{"token": {"getUserWiseLiveData"}, "user": {username}, "pass": {password}}
	resp, err := c.http.Get(c.baseURL + "/webservice?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("sumithlive: getUserWiseLiveData: %w", err)
	}
	defer resp.Body.Close()
	var out []Vehicle
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("sumithlive: decode getUserWiseLiveData response: %w", err)
	}
	return out, nil
}

func (c *Client) post(token, authCode string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("sumithlive: encode request: %w", err)
	}
	url := fmt.Sprintf("%s/webservice?token=%s", c.baseURL, token)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("sumithlive: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authCode != "" {
		req.Header.Set("auth-code", authCode)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sumithlive: request %s: %w", token, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sumithlive: %s returned HTTP %d", token, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("sumithlive: decode %s response: %w", token, err)
	}
	return nil
}
