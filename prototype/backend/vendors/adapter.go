// Package vendors defines the adapter contract every vendor integration
// implements, and a registry that looks adapters up by name. Adding a
// vendor is: write vendors/<name>/adapter.go, register it in main.go.
// Nothing else in the codebase needs to change.
package vendors

import (
	"context"
	"fmt"

	"mediamtx-console/domain"
)

type Adapter interface {
	Name() string
	ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error)
	ListCameras(ctx context.Context, vendorParams map[string]string) ([]domain.Camera, error)
}

type Registry struct {
	adapters map[string]Adapter
}

func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: make(map[string]Adapter, len(adapters))}
	for _, a := range adapters {
		r.adapters[a.Name()] = a
	}
	return r
}

func (r *Registry) Get(vendor string) (Adapter, error) {
	a, ok := r.adapters[vendor]
	if !ok {
		return nil, fmt.Errorf("unknown vendor %q", vendor)
	}
	return a, nil
}

// All returns every registered adapter, for callers that need to sweep
// across vendors (e.g. building a fleet-wide roster) rather than look one
// up by name.
func (r *Registry) All() []Adapter {
	all := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		all = append(all, a)
	}
	return all
}
