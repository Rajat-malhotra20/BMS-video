package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Bus maps one bus id to the vendor serving its cameras and whatever
// device identifiers that vendor needs (terid/dsno/plateNo, channel, ...).
// This is what lets the frontend say "start bus DL1PC0001 cam 1" without
// knowing or caring which vendor is behind it.
type Bus struct {
	Vendor       string            `json:"vendor"` // registry key: "castmaster", "n9m", "sumithlive"
	VendorParams map[string]string `json:"vendorParams"`
}

// LoadBuses reads the bus->vendor assignment from a JSON file
// ({"DL1PC0001": {"vendor": "castmaster", "vendorParams": {"terid": "..."}}}).
func LoadBuses(path string) (map[string]Bus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var buses map[string]Bus
	if err := json.Unmarshal(data, &buses); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return buses, nil
}
