package castmaster

import "net/http"

// AlarmCount is one (device, day) alarm tally.
type AlarmCount struct {
	Terid string `json:"terid"`
	Date  string `json:"date"` // yyyy-MM-dd
	Count int    `json:"count"`
}

// AlarmCount queries the total number of alarms of one type, per device per
// day, over a time range. See Alarm Type table in the doc (e.g. 13 = EMR/
// Urgency).
// POST /api/v1/basic/alarm/count
func (c *Client) AlarmCount(terids []string, alarmType int, startTime, endTime string) ([]AlarmCount, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     terids,
		"type":      alarmType,
		"starttime": startTime,
		"endtime":   endTime,
	}
	var out []AlarmCount
	err := c.postJSON(http.MethodPost, "/api/v1/basic/alarm/count", body, &out)
	return out, err
}

// AlarmDetail is one alarm event record.
type AlarmDetail struct {
	Terid       string `json:"terid"`
	GPSTime     string `json:"gpstime"`
	Altitude    int    `json:"altitude"`
	Direction   int    `json:"direction"`
	GPSLat      string `json:"gpslat"`
	GPSLng      string `json:"gpslng"`
	Speed       int    `json:"speed"`
	RecordSpeed int    `json:"recordspeed"`
	State       int    `json:"state"`
	Time        string `json:"time"`
	Type        int    `json:"type"`
	Content     string `json:"content"`
	CmdType     int    `json:"cmdtype"` // 2: alarm, 1: alarm relief
}

// AlarmDetail queries individual alarm events for one or more devices over a
// time range. types may be empty ([]int{}) to include all alarm types.
// POST /api/v1/basic/alarm/detail
func (c *Client) AlarmDetail(terids []string, types []int, startTime, endTime string) ([]AlarmDetail, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     terids,
		"type":      types,
		"starttime": startTime,
		"endtime":   endTime,
	}
	var out []AlarmDetail
	err := c.postJSON(http.MethodPost, "/api/v1/basic/alarm/detail", body, &out)
	return out, err
}

// StateLogEntry is one online/offline transition.
type StateLogEntry struct {
	Terid string `json:"terid"`
	Time  string `json:"time"`
	Type  int    `json:"type"` // 0: offline, 1: online
}

// StateLog queries the online/offline history for one or more devices over
// a time range. POST /api/v1/basic/state/log
func (c *Client) StateLog(terids []string, startTime, endTime string) ([]StateLogEntry, error) {
	body := map[string]any{"key": c.key, "terid": terids, "starttime": startTime, "endtime": endTime}
	var out []StateLogEntry
	err := c.postJSON(http.MethodPost, "/api/v1/basic/state/log", body, &out)
	return out, err
}

// OnlineDevice is a currently-online device serial number.
type OnlineDevice struct {
	Terid string `json:"terid"`
}

// StateNow queries which of the given devices are online right now. Pass an
// empty slice (as a super-admin) to list all currently online devices.
// POST /api/v1/basic/state/now
func (c *Client) StateNow(terids []string) ([]OnlineDevice, error) {
	body := map[string]any{"key": c.key, "terid": terids}
	var out []OnlineDevice
	err := c.postJSON(http.MethodPost, "/api/v1/basic/state/now", body, &out)
	return out, err
}

// LastState is the timestamp a device was last online.
type LastState struct {
	Terid string `json:"terid"`
	Time  string `json:"time"`
}

// StateLast queries the last-known-online time for one or more devices.
// POST /api/v1/basic/state/last
func (c *Client) StateLast(terids []string) ([]LastState, error) {
	body := map[string]any{"key": c.key, "terid": terids}
	var out []LastState
	err := c.postJSON(http.MethodPost, "/api/v1/basic/state/last", body, &out)
	return out, err
}

// PassengerFlowEvent is one door-open passenger count event.
type PassengerFlowEvent struct {
	CloseTime string  `json:"closetime"`
	Door      string  `json:"door"`
	Off       int     `json:"off"` // alighted
	On        int     `json:"on"`  // boarded
	OpenTime  string  `json:"opentime"`
	SiteName  string  `json:"sitename"`
	Terid     string  `json:"terid"`
	Time      string  `json:"time"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
}

// PassengerFlowDetail queries passenger-counting events for one or more
// devices over a time range. door may be "" to include all doors.
// POST /api/v1/basic/passenger-count/detail
func (c *Client) PassengerFlowDetail(terids []string, startTime, endTime, door string) ([]PassengerFlowEvent, error) {
	body := map[string]any{
		"key":       c.key,
		"terid":     terids,
		"starttime": startTime,
		"endtime":   endTime,
		"door":      door,
	}
	var out []PassengerFlowEvent
	err := c.postJSON(http.MethodPost, "/api/v1/basic/passenger-count/detail", body, &out)
	return out, err
}
