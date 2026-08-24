package n9m

import (
	"encoding/json"
	"fmt"
	"time"
)

// This file adds Conn helper methods for the DEVEMM device-status/control
// commands (see devicestatus.go for the message types).

// QueryGeneralStatus sends DEVEMM/QUERYDEVGENERALSTATUS and returns the
// decoded status.
func (c *Conn) QueryGeneralStatus(sessionID string, q GeneralStatusQuery, timeout time.Duration) (GeneralStatusResponse, error) {
	env, err := c.Request(ModuleDeviceEM, OpQueryGeneralStatus, sessionID, q, timeout)
	if err != nil {
		return GeneralStatusResponse{}, err
	}
	var resp GeneralStatusResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return GeneralStatusResponse{}, fmt.Errorf("n9m: decode QUERYDEVGENERALSTATUS response: %w", err)
	}
	return resp, nil
}

// GetDevInfoStatus sends DEVEMM/GETDEVINFOSTATUS and returns the decoded
// status.
func (c *Conn) GetDevInfoStatus(sessionID string, serial int, timeout time.Duration) (GetDevInfoStatusResponse, error) {
	env, err := c.Request(ModuleDeviceEM, OpGetDevInfoStatus, sessionID, map[string]int{"SERIAL": serial}, timeout)
	if err != nil {
		return GetDevInfoStatusResponse{}, err
	}
	var resp GetDevInfoStatusResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return GetDevInfoStatusResponse{}, fmt.Errorf("n9m: decode GETDEVINFOSTATUS response: %w", err)
	}
	return resp, nil
}

// GetUpdateIOStatus sends DEVEMM/GETUPDATEIOSTATUS and returns the decoded
// I/O + ACC + pulse + panic-button status.
func (c *Conn) GetUpdateIOStatus(sessionID string, serial int, timeout time.Duration) (IOStatusPayload, error) {
	env, err := c.Request(ModuleDeviceEM, OpGetUpdateIOStatus, sessionID, map[string]int{"SERIAL": serial}, timeout)
	if err != nil {
		return IOStatusPayload{}, err
	}
	var resp IOStatusPayload
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return IOStatusPayload{}, fmt.Errorf("n9m: decode GETUPDATEIOSTATUS response: %w", err)
	}
	return resp, nil
}

// PushIOStatus sends DEVEMM/UPDATEIOSTATUS, the device-initiated
// fire-and-forget notification sent whenever I/O status changes (the doc
// defines no reply for this operation).
func (c *Conn) PushIOStatus(sessionID string, p IOStatusPayload) error {
	return c.SendCommand(ModuleDeviceEM, OpUpdateIOStatus, sessionID, p)
}

// SetControlDevCmd sends DEVEMM/SETCONTROLDEVCMD (restart/sleep-poweroff/
// poweroff) and returns the device's acknowledgement.
func (c *Conn) SetControlDevCmd(sessionID string, p SetControlDevCmdParams, timeout time.Duration) (SetControlDevCmdResponse, error) {
	env, err := c.Request(ModuleDeviceEM, OpSetControlDevCmd, sessionID, p, timeout)
	if err != nil {
		return SetControlDevCmdResponse{}, err
	}
	var resp SetControlDevCmdResponse
	if err := json.Unmarshal(env.Response, &resp); err != nil {
		return SetControlDevCmdResponse{}, fmt.Errorf("n9m: decode SETCONTROLDEVCMD response: %w", err)
	}
	if resp.ErrorCode != 0 {
		return resp, fmt.Errorf("n9m: SETCONTROLDEVCMD failed: code=%d cause=%s", resp.ErrorCode, resp.ErrorCause)
	}
	return resp, nil
}
