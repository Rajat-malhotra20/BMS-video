// Package config loads the vendor account credentials this service needs
// for its own vendors (castmaster). It reads the same vendors.json the
// fleet API reads — one file, one set of credentials, mounted into both.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// VendorAccount is one vendor's account-level credentials/base URL.
type VendorAccount struct {
	BaseURL  string            `json:"baseUrl"`
	Username string            `json:"username"`
	Password string            `json:"password"`
	Extra    map[string]string `json:"extra,omitempty"`
}

// LoadVendors reads vendor account config from a JSON file. A password
// left empty in the file is filled from VENDOR_<NAME>_PASSWORD
// (upper-cased) so secrets don't need to live in a checked-in file.
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
