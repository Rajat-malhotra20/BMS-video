// Package config loads the two static config files that let the frontend
// call the bridge without ever naming a vendor: vendors.json (per-vendor
// account credentials, shared across every bus using that vendor) and
// buses.json (per-bus vendor assignment + device identifiers).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// VendorAccount is one vendor's account-level credentials/base URL —
// shared by every bus that uses this vendor. Per-bus identifiers
// (terid/dsno/plateNo) live in Bus.VendorParams instead, since those
// differ per device even within one vendor account.
type VendorAccount struct {
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Extra holds vendor-specific defaults that don't fit the common
	// fields above — e.g. sumithlive's account-wide "projectId", needed
	// when auto-discovering the account's full vehicle roster (there's no
	// per-vehicle project id in that listing call).
	Extra map[string]string `json:"extra,omitempty"`
}

// LoadVendors reads vendor account config from a JSON file
// ({"castmaster": {"baseUrl": ..., "username": ..., "password": ...}, ...}).
// A password left empty in the file is filled from
// VENDOR_<NAME>_PASSWORD (upper-cased) so secrets don't need to live in a
// checked-in file.
func LoadVendors(path string) (map[string]VendorAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var vendors map[string]VendorAccount
	if err := json.Unmarshal(data, &vendors); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	for name, acct := range vendors {
		if acct.Password == "" {
			if p := os.Getenv("VENDOR_" + strings.ToUpper(name) + "_PASSWORD"); p != "" {
				acct.Password = p
				vendors[name] = acct
			}
		}
	}
	return vendors, nil
}
