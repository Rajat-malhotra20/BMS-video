package sumith

import "strings"

// This file covers the doc's SMS-based command channel (sections 152-159 and
// scattered SMS examples throughout). SMS commands ride over plain text
// messages rather than the $...# framed protocol used by TCP/GPRS, but they
// reuse the same "TOKEN,field1,field2,..." shape without checksum or
// envelope markers. A single generic builder/parser covers all of them; the
// device-specific behavior lives entirely in which token and how many
// fields are supplied.

// SMSCommand is a decoded (or to-be-encoded) SMS command: a token followed
// by an arbitrary number of comma-separated fields.
type SMSCommand struct {
	Token  string
	Fields []string
}

// BuildSMSCommand renders an SMS command as "TOKEN,field1,field2,...".
func BuildSMSCommand(token string, fields ...string) string {
	if len(fields) == 0 {
		return token
	}
	return token + "," + strings.Join(fields, ",")
}

// ParseSMSCommand splits a raw SMS command body into its token and fields.
// Unlike Parse (for $...# frames) there is no checksum or trailing marker.
func ParseSMSCommand(raw string) (SMSCommand, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SMSCommand{}, &ErrMalformed{Raw: raw}
	}
	parts := strings.Split(raw, ",")
	return SMSCommand{Token: parts[0], Fields: parts[1:]}, nil
}

// SMS command tokens, both directions (server->device requests and
// device->server responses share the same token per the doc's SMS section).
const (
	SMSSetAPN             = "SETAPN"
	SMSGetAPN             = "GETAPN"
	SMSSetIP1             = "SETIP1"
	SMSGetIP1             = "GETIP1"
	SMSSetIP2             = "SETIP2"
	SMSGetIP2             = "GETIP2"
	SMSSetVehicleRegNum   = "SETVEHREG"
	SMSGetVehicleRegNum   = "GETVEHREG"
	SMSSetIgnitionConfig  = "SETIGNCFG"
	SMSGetIgnitionConfig  = "GETIGNCFG"
	SMSSetEmergencyConfig = "SETEMRCFG"
	SMSGetEmergencyConfig = "GETEMRCFG"
	SMSSetDeviceReset     = "SETDEVRST"
	SMSGetIMEI            = "GETIMEI"
	SMSClearAllConfig     = "CLRCFGALL"
	SMSClearSpeedAlarm    = "CLRALMSPEED"
	SMSSetPhoneNumber     = "SETPHNUM"
	SMSGetPhoneNumber     = "GETPHNUM"
	SMSGetOverSpeedLimit  = "GETOSLIMIT"
	SMSSetOverSpeedLimit  = "SETOSLIMIT"
	SMSGetHarshAccel      = "GETHA"
	SMSSetHarshAccel      = "SETHA"
	SMSGetHarshBreak      = "GETHB"
	SMSSetHarshBreak      = "SETHB"
	SMSGetRashTurn        = "GETRT"
	SMSSetRashTurn        = "SETRT"
	SMSSetNormalTime      = "SETNORMALTIME"
	SMSGetNormalTime      = "GETNORMALTIME"
	SMSGetIgnitionDelay   = "GETIGNDELAYCFG"
	SMSSetIgnitionDelay   = "SETIGNDELAYCFG"
)

// Convenience wrappers for the single-value get/set pairs — every "SET*"
// token takes exactly one field, every "GET*" token takes none.

func BuildSMSSetAPN(apn string) string            { return BuildSMSCommand(SMSSetAPN, apn) }
func BuildSMSGetAPN() string                      { return BuildSMSCommand(SMSGetAPN) }
func BuildSMSSetIP1(ip string, port int) string   { return BuildSMSCommand(SMSSetIP1, ip, itoa(port)) }
func BuildSMSGetIP1() string                      { return BuildSMSCommand(SMSGetIP1) }
func BuildSMSSetIP2(ip string, port int) string   { return BuildSMSCommand(SMSSetIP2, ip, itoa(port)) }
func BuildSMSGetIP2() string                      { return BuildSMSCommand(SMSGetIP2) }
func BuildSMSSetVehicleRegNum(reg string) string  { return BuildSMSCommand(SMSSetVehicleRegNum, reg) }
func BuildSMSGetVehicleRegNum() string            { return BuildSMSCommand(SMSGetVehicleRegNum) }
func BuildSMSSetDeviceReset() string              { return BuildSMSCommand(SMSSetDeviceReset) }
func BuildSMSGetIMEI() string                     { return BuildSMSCommand(SMSGetIMEI) }
func BuildSMSClearAllConfig() string              { return BuildSMSCommand(SMSClearAllConfig) }
func BuildSMSClearSpeedAlarm() string             { return BuildSMSCommand(SMSClearSpeedAlarm) }
func BuildSMSSetPhoneNumber(number string) string { return BuildSMSCommand(SMSSetPhoneNumber, number) }
func BuildSMSGetPhoneNumber() string              { return BuildSMSCommand(SMSGetPhoneNumber) }
func BuildSMSGetOverSpeedLimit() string           { return BuildSMSCommand(SMSGetOverSpeedLimit) }
func BuildSMSSetOverSpeedLimit(kmh int) string {
	return BuildSMSCommand(SMSSetOverSpeedLimit, itoa(kmh))
}
func BuildSMSGetHarshAccel() string          { return BuildSMSCommand(SMSGetHarshAccel) }
func BuildSMSSetHarshAccel(value int) string { return BuildSMSCommand(SMSSetHarshAccel, itoa(value)) }
func BuildSMSGetHarshBreak() string          { return BuildSMSCommand(SMSGetHarshBreak) }
func BuildSMSSetHarshBreak(value int) string { return BuildSMSCommand(SMSSetHarshBreak, itoa(value)) }
func BuildSMSGetRashTurn() string            { return BuildSMSCommand(SMSGetRashTurn) }
func BuildSMSSetRashTurn(value int) string   { return BuildSMSCommand(SMSSetRashTurn, itoa(value)) }
func BuildSMSSetNormalTime(startHHmm, endHHmm string) string {
	return BuildSMSCommand(SMSSetNormalTime, startHHmm, endHHmm)
}
func BuildSMSGetNormalTime() string { return BuildSMSCommand(SMSGetNormalTime) }
func BuildSMSSetIgnitionDelay(seconds int) string {
	return BuildSMSCommand(SMSSetIgnitionDelay, itoa(seconds))
}
func BuildSMSGetIgnitionDelay() string { return BuildSMSCommand(SMSGetIgnitionDelay) }

// BuildSMSSetIgnitionConfig / BuildSMSSetEmergencyConfig take the doc's
// variable-length config field lists as-is (their internal shape isn't
// pinned down by a single worked example in the source PDF).
func BuildSMSSetIgnitionConfig(fields ...string) string {
	return BuildSMSCommand(SMSSetIgnitionConfig, fields...)
}
func BuildSMSGetIgnitionConfig() string { return BuildSMSCommand(SMSGetIgnitionConfig) }
func BuildSMSSetEmergencyConfig(fields ...string) string {
	return BuildSMSCommand(SMSSetEmergencyConfig, fields...)
}
func BuildSMSGetEmergencyConfig() string { return BuildSMSCommand(SMSGetEmergencyConfig) }
