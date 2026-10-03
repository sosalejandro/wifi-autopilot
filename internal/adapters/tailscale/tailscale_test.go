package tailscale

import (
	"context"
	"testing"

	"github.com/sosalejandro/wifi-autopilot/internal/core/ports"
)

var _ ports.ExitNodes = (*Adapter)(nil)

func fake(out string) *Adapter {
	return &Adapter{Run: func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }}
}

const withExitNode = `{
  "ExitNodeStatus": {"ID": "nX", "Online": false, "TailscaleIPs": ["100.64.0.1/32"]},
  "Peer": {
    "k1": {"ID": "nX", "HostName": "ubuntu", "DNSName": "exit-1.example.ts.net.", "Online": false},
    "k2": {"ID": "nY", "HostName": "laptop", "DNSName": "laptop.example.ts.net.", "Online": true}
  }}`

func TestCurrent(t *testing.T) {
	en, err := fake(withExitNode).Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if en == nil || en.Name != "exit-1" || en.Online {
		t.Fatalf("got %+v", en)
	}
	if en, err := fake(`{"Peer": {}}`).Current(context.Background()); err != nil || en != nil {
		t.Fatalf("no exit node: got %+v, %v", en, err)
	}
}

func TestPeerOnline(t *testing.T) {
	a := fake(withExitNode)
	if up, _ := a.PeerOnline(context.Background(), "laptop"); !up {
		t.Error("laptop should be online")
	}
	if up, _ := a.PeerOnline(context.Background(), "exit-1"); up {
		t.Error("exit-1 should be offline")
	}
}
