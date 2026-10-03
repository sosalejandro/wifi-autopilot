package networkmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
	"github.com/sosalejandro/wifi-autopilot/internal/core/ports"
)

var _ ports.WiFi = (*Adapter)(nil)

func TestParseScan(t *testing.T) {
	out := []byte("Home_5G:00\\:00\\:5E\\:00\\:53\\:95:153:5765 MHz:1170 Mbit/s:80 MHz:59:WPA2:*\n" +
		"Neighbor Guest:00\\:00\\:5E\\:00\\:53\\:52:7:2442 MHz:1170 Mbit/s:40 MHz:70:WPA2: \n" +
		"we\\:ird:00\\:11\\:22\\:33\\:44\\:55:1:2412 MHz:130 Mbit/s:20 MHz:22:WPA2 WPA3: \n")
	aps, err := parseScan(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 3 {
		t.Fatalf("got %d APs", len(aps))
	}
	a := aps[0]
	if a.SSID != "Home_5G" || a.BSSID != "00:00:5E:00:53:95" || a.Channel != 153 ||
		a.FreqMHz != 5765 || a.WidthMHz != 80 || a.Signal != 59 || !a.InUse || a.Band() != domain.Band5G {
		t.Errorf("bad parse: %+v", a)
	}
	if aps[1].InUse || aps[1].Band() != domain.Band2G {
		t.Errorf("bad parse: %+v", aps[1])
	}
	if aps[2].SSID != "we:ird" || aps[2].RateMbps != 130 {
		t.Errorf("escaped colon: %+v", aps[2])
	}
}

func TestSyncProfiles(t *testing.T) {
	dir := t.TempDir()
	var calls []string
	a := &Adapter{Iface: "wlan0", ProfileDir: dir, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}}
	stale := filepath.Join(dir, profileFilePrefix+"gone-000000.nmconnection")
	os.WriteFile(stale, nil, 0o600)
	foreign := filepath.Join(dir, "Home.nmconnection")
	os.WriteFile(foreign, nil, 0o600)

	networks := []domain.Network{
		{SSID: "Home Network", Password: `pa\ssword`, Security: "wpa-psk"},
		{SSID: "Cafe", Security: "open"},
	}
	n, err := a.SyncProfiles(context.Background(), networks)
	if err != nil || n != 3 {
		t.Fatalf("first sync: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale managed profile not removed")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign profile was touched")
	}
	path := filepath.Join(dir, profileFile("Home Network"))
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "id=wifi-autopilot/Home Network\n") || !strings.Contains(string(data), `psk=pa\\ssword`) {
		t.Errorf("bad keyfile:\n%s", data)
	}
	if cafe, _ := os.ReadFile(filepath.Join(dir, profileFile("Cafe"))); strings.Contains(string(cafe), "wifi-security") {
		t.Error("open network should have no security section")
	}
	if len(calls) != 1 || calls[0] != "nmcli connection reload" {
		t.Errorf("expected one reload, got %v", calls)
	}

	calls = nil
	if n, _ := a.SyncProfiles(context.Background(), networks); n != 0 || len(calls) != 0 {
		t.Errorf("second sync should be a no-op: n=%d calls=%v", n, calls)
	}
}
