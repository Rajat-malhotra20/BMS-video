// Package chemitoapi is a Go client for Chemito's "CM Server Application"
// HTTP API (docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx). Same underlying
// OEM platform/template as vendorclients/castmaster (identical envelope
// shape and error-code appendix), but NOT wire-compatible: login is
// POST+JSON-body here (GET+query-params for castmaster), and the live-video
// call passes the key as a query param instead of a header. Only the
// live-video path is implemented — GPS (§2) and alarm (§3) interfaces are
// documented but unused by this project today.
package chemitoapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client talks to a single Chemito CM server instance (e.g.
// "http://15.252.130.159:12056" per the doc — note plain HTTP, not TLS).
type Client struct {
	BaseURL    string
	HTTPClient *http.Client

	key string
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{BaseURL: baseURL, HTTPClient: httpClient}
}

// errorCodeDescriptions is Appendix 1 of the doc — the numeric column
// lives in the spreadsheet's cell values, not its shared strings, so an
// earlier text-only extraction missed this table entirely. Transcribed
// 2026-08-18 from docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx rows 159-190.
var errorCodeDescriptions = map[int]string{
	200: "Request successful / Response successful",
	201: "Illegal request",
	202: "Server Error",
	203: "No authority",
	204: "Authorization expired",
	205: "Account has expired",
	206: "Username and password are incorrect",
	207: "Request parameter number exception",
	208: "Request format error",
	209: "Unauthorized key detected",
	210: "Authorization key error",
	211: "MD5 error",
	212: "No data",
	213: "No device",
	214: "No space",
	215: "No file",
	216: "Request parameter content does not meet the restrictions",
	217: "User logged in",
	218: "Account lockout",
	219: "Password expiration",
	220: "Low password strength",
	224: "authorization_expired",
	225: "api_unauthorized",
	226: "device_limit",
	300: "Database connection error",
	301: "Database operation exception",
	302: "Internal interface parameter number error",
	400: "Terminal search video calendar fail",
	401: "Terminal is not Online",
	402: "The terminal retrieval service is busy",
	403: "Terminal execution fail",
}

// APIError is returned when the server responds with a non-success
// errorcode (Appendix 1 of the doc).
type APIError struct {
	Code  int
	Cause string
}

func (e *APIError) Error() string {
	if e.Cause != "" {
		return fmt.Sprintf("chemitoapi: errorcode=%d: %s", e.Code, e.Cause)
	}
	if desc, ok := errorCodeDescriptions[e.Code]; ok {
		return fmt.Sprintf("chemitoapi: errorcode=%d: %s", e.Code, desc)
	}
	return fmt.Sprintf("chemitoapi: errorcode=%d", e.Code)
}

type envelope struct {
	Data      json.RawMessage `json:"data"`
	ErrorCode int             `json:"errorcode"`
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("chemitoapi: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("chemitoapi: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chemitoapi: http %d: %s", resp.StatusCode, string(body))
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("chemitoapi: decode response: %w (body=%s)", err, string(body))
	}
	if env.ErrorCode != 200 && env.ErrorCode != 0 {
		return &APIError{Code: env.ErrorCode}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("chemitoapi: decode data: %w", err)
		}
	}
	return nil
}

// Login exchanges a username/password for a verify key (§1.1).
// POST /api/v1/basic/key — body {"username","password"}, unlike
// castmaster's GET+query-param login.
func (c *Client) Login(username, password string) (string, error) {
	buf, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", fmt.Errorf("chemitoapi: encode request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/v1/basic/key", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var out struct {
		Key string `json:"key"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	c.key = out.Key
	return c.key, nil
}

// VideoPort is one available live-preview relay port.
//
// The "Get video port information" call is referenced by the doc's §4
// operation steps but not itself documented in
// docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx. This mirrors castmaster's
// equivalent endpoint (same OEM platform, identical envelope/error-code
// shape) — confirmed live against the real Chemito server (2026-08-18,
// account "admin"): GET /api/v1/basic/live/port returns
// {"data":[],"errorcode":200}. The endpoint is real; an empty result means
// no device currently has an active live relay — a device can be
// registered (see ListDevices) without one, e.g. it's offline right now.
func (c *Client) LivePorts() ([]VideoPort, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/v1/basic/live/port", nil)
	if err != nil {
		return nil, err
	}
	var out []VideoPort
	err = c.do(req, &out)
	return out, err
}

type VideoPort struct {
	Port int `json:"port"`
}

// Device is one entry from ListDevices.
type Device struct {
	Terid        string `json:"terid"`
	CarLicence   string `json:"carlicence"`
	ChannelCount int    `json:"channelcount"`
	DeviceType   int    `json:"devicetype"`
}

// ListDevices returns every device registered on this account — this is
// the "'Get device list' interface" the doc's §4 operation steps reference
// without ever documenting: not in docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx
// or the fuller castmaster doc either, but confirmed live (2026-08-18)
// against the real Chemito server at GET /api/v1/basic/devices. The
// terid/type/starttime/endtime body fields it accepts are decoys — tested
// with the real terid, an empty array, no body at all, and a bogus terid;
// all four returned the identical, unfiltered full device list. Passing
// none of them (nil body) here on purpose.
func (c *Client) ListDevices() ([]Device, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/v1/basic/devices?key="+url.QueryEscape(c.key), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var out []Device
	err = c.do(req, &out)
	return out, err
}

// LiveStreamType selects the codestream for the live-video endpoint:
// 0 = main codestream, 1 = sub codestream (§4, same convention as
// castmaster's LiveStreamType).
type LiveStreamType int

const (
	LiveStreamMain LiveStreamType = 0
	LiveStreamSub  LiveStreamType = 1
)

// LiveVideoURL requests the playable FLV URL for one device channel (§4).
// GET /api/v1/basic/live/video — key travels as a query param here, not a
// header (unlike castmaster).
func (c *Client) LiveVideoURL(terid string, channel int, audio bool, st LiveStreamType, port int) (string, error) {
	q := url.Values{
		"key":   {c.key},
		"terid": {terid},
		"chl":   {strconv.Itoa(channel)},
		"audio": {boolToFlag(audio)},
		"st":    {strconv.Itoa(int(st))},
		"port":  {strconv.Itoa(port)},
	}
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/v1/basic/live/video?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func boolToFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
