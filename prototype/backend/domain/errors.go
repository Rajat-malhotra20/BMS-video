package domain

import "fmt"

// VendorError normalizes a failure from any adapter so the HTTP layer can
// map it to a status code in one place instead of string-matching errors.
type VendorError struct {
	Vendor    string
	Op        string // what was being attempted, e.g. "login", "resolve live url"
	Code      string // vendor-agnostic reason, e.g. "device_offline", "not_implemented"
	Retryable bool
	Cause     error
}

func (e *VendorError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Vendor, e.Op, e.Cause)
	}
	return fmt.Sprintf("%s: %s: %s", e.Vendor, e.Op, e.Code)
}

func (e *VendorError) Unwrap() error { return e.Cause }

// WrapVendorErr wraps a raw vendorclients error with the vendor/op context
// needed for logging and status-code mapping. Returns nil if err is nil.
func WrapVendorErr(vendor, op string, err error) error {
	if err == nil {
		return nil
	}
	return &VendorError{Vendor: vendor, Op: op, Cause: err}
}

// ErrNotImplemented marks an adapter method whose vendor-side contract
// isn't nailed down yet (e.g. Chemito's HTTP API, pending their spec).
func ErrNotImplemented(vendor, op string) error {
	return &VendorError{Vendor: vendor, Op: op, Code: "not_implemented"}
}
