package chemitoapi

import (
	"testing"

	rawclient "mediamtx-console/vendorclients/chemitoapi"
)

// The doc (PMIDTC_CIPLAPIS.xlsx §4) caps a port at 4 channels, and the
// account exposes 4 ports — so 16 channels fit only if they are spread.
// This used to hand every channel ports[0], which put 12 channels on a
// port that holds 4 and made the overflow look like a dead device.
func TestPortForSpreadsAndIsStable(t *testing.T) {
	ports := []rawclient.VideoPort{{Port: 12060}, {Port: 12061}, {Port: 12062}, {Port: 12063}}
	a := New(Config{})

	keys := []string{
		"devA_1", "devA_2", "devA_3", "devA_4",
		"devB_1", "devB_2", "devB_3", "devB_4",
	}
	load := map[int]int{}
	assigned := map[string]int{}
	for _, k := range keys {
		p := a.portFor(ports, k)
		assigned[k] = p
		load[p]++
	}

	for _, vp := range ports {
		if load[vp.Port] != 2 {
			t.Fatalf("port %d carries %d channels, want an even 2 (load=%v)", vp.Port, load[vp.Port], load)
		}
	}

	// A reconnect must land on the same port, or the vendor sees the
	// channel move and the old slot leaks.
	for k, want := range assigned {
		if got := a.portFor(ports, k); got != want {
			t.Fatalf("%s moved from port %d to %d on reconnect", k, want, got)
		}
	}
}

// A port the account stops offering must not pin a channel to a dead port.
func TestPortForDropsVanishedPort(t *testing.T) {
	a := New(Config{})
	if p := a.portFor([]rawclient.VideoPort{{Port: 12060}}, "devA_1"); p != 12060 {
		t.Fatalf("port = %d, want 12060", p)
	}
	if p := a.portFor([]rawclient.VideoPort{{Port: 12071}}, "devA_1"); p != 12071 {
		t.Fatalf("port = %d, want 12071 after 12060 vanished", p)
	}
}
