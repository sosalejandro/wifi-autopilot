package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

type fakeWiFi struct {
	aps       []domain.AP
	saved     map[string]string
	connected bool
	connects  []string // "profile@bssid"
	synced    []domain.Network
}

func (f *fakeWiFi) State(context.Context) (domain.DeviceState, error) {
	return domain.DeviceState{WifiConnected: f.connected}, nil
}
func (f *fakeWiFi) Scan(context.Context, bool) ([]domain.AP, error) { return f.aps, nil }
func (f *fakeWiFi) SavedNetworks(context.Context) (map[string]string, error) {
	return f.saved, nil
}
func (f *fakeWiFi) Connect(_ context.Context, profile, bssid string) error {
	f.connects = append(f.connects, profile+"@"+bssid)
	for i := range f.aps {
		f.aps[i].InUse = f.aps[i].BSSID == bssid
	}
	f.connected = true
	return nil
}
func (f *fakeWiFi) SyncProfiles(_ context.Context, n []domain.Network) (int, error) {
	f.synced = n
	return 0, nil
}

type fakeExitNodes struct {
	current *domain.ExitNode
	peerUp  bool
	sets    []string
}

func (f *fakeExitNodes) Current(context.Context) (*domain.ExitNode, error) { return f.current, nil }
func (f *fakeExitNodes) PeerOnline(context.Context, string) (bool, error)  { return f.peerUp, nil }
func (f *fakeExitNodes) Set(_ context.Context, name string) error {
	f.sets = append(f.sets, name)
	if name == "" {
		f.current = nil
	} else {
		f.current = &domain.ExitNode{Name: name, Online: true}
	}
	return nil
}

type fakeProber struct {
	internet func() bool
	dns      bool
}

func (f *fakeProber) Internet(context.Context, domain.Connectivity) bool { return f.internet() }
func (f *fakeProber) DNS(context.Context, domain.Connectivity) bool      { return f.dns }

type fakeDNS struct {
	repairs int
	fixes   *fakeProber
}

func (f *fakeDNS) Repair(ctx context.Context, _ time.Duration, ok func(context.Context) bool) error {
	f.repairs++
	f.fixes.dns = true
	if !ok(ctx) {
		panic("repair should have fixed DNS")
	}
	return nil
}

type fakeConfig struct{ cfg domain.Config }

func (f *fakeConfig) Load(context.Context) (*domain.Config, error) { c := f.cfg; return &c, nil }
func (f *fakeConfig) Changed() bool                                { return false }

type memState map[string]string

func (m memState) Get(k string) string   { return m[k] }
func (m memState) Set(k, v string) error { m[k] = v; return nil }

type harness struct {
	*Autopilot
	wifi  *fakeWiFi
	exits *fakeExitNodes
	probe *fakeProber
	dns   *fakeDNS
	state memState
	clock time.Time
}

func newHarness(t *testing.T, aps []domain.AP, mutate func(*domain.Config)) *harness {
	t.Helper()
	cfg := domain.DefaultConfig()
	cfg.Networks = []domain.Network{{SSID: "home", Weight: 1}, {SSID: "home_5G", Weight: 1}}
	if mutate != nil {
		mutate(&cfg)
	}
	h := &harness{
		wifi:  &fakeWiFi{aps: aps, saved: map[string]string{"home": "home", "home_5G": "home_5G"}, connected: true},
		exits: &fakeExitNodes{},
		probe: &fakeProber{internet: func() bool { return true }, dns: true},
		state: memState{},
		clock: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	h.dns = &fakeDNS{fixes: h.probe}
	a, err := New(context.Background(), Deps{
		WiFi: h.wifi, ExitNodes: h.exits, Prober: h.probe, DNS: h.dns,
		Config: &fakeConfig{cfg}, State: h.state,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:   func() time.Time { return h.clock },
		Sleep: func(_ context.Context, d time.Duration) error { h.clock = h.clock.Add(d); return nil },
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	h.Autopilot = a
	return h
}

func (h *harness) tick(advance time.Duration) {
	h.clock = h.clock.Add(advance)
	h.Tick(context.Background())
}

var (
	weak24 = domain.AP{SSID: "home", BSSID: "AA:00:00:00:00:01", Channel: 1, FreqMHz: 2412, WidthMHz: 20, Signal: 60, InUse: true}
	good5  = domain.AP{SSID: "home_5G", BSSID: "AA:00:00:00:00:02", Channel: 36, FreqMHz: 5180, WidthMHz: 80, Signal: 80}
)

func TestRoamsOnlyAfterConfirmation(t *testing.T) {
	h := newHarness(t, []domain.AP{weak24, good5}, nil)

	h.tick(0)
	if len(h.wifi.connects) != 0 {
		t.Fatalf("switched on first sighting: %v", h.wifi.connects)
	}
	h.tick(30 * time.Second)
	if len(h.wifi.connects) != 1 || h.wifi.connects[0] != "home_5G@"+good5.BSSID {
		t.Fatalf("expected switch to 5 GHz after confirmation, got %v", h.wifi.connects)
	}
}

func TestStaysPutWhenGainIsSmall(t *testing.T) {
	similar := good5
	similar.SSID, similar.BSSID, similar.FreqMHz, similar.Channel, similar.InUse = "home", "AA:00:00:00:00:03", 5200, 40, true
	similar.Signal = 78
	h := newHarness(t, []domain.AP{similar, good5}, nil)
	for i := 0; i < 5; i++ {
		h.tick(30 * time.Second)
	}
	if len(h.wifi.connects) != 0 {
		t.Fatalf("flapped between near-identical APs: %v", h.wifi.connects)
	}
}

func TestConnectsImmediatelyWhenDisconnected(t *testing.T) {
	idle := weak24
	idle.InUse = false
	h := newHarness(t, []domain.AP{idle, good5}, nil)
	h.wifi.connected = false
	h.tick(0)
	if len(h.wifi.connects) != 1 || h.wifi.connects[0] != "home_5G@"+good5.BSSID {
		t.Fatalf("expected immediate connect to best AP, got %v", h.wifi.connects)
	}
}

func TestFailedSwitchRollsBackAndBlacklists(t *testing.T) {
	h := newHarness(t, []domain.AP{weak24, good5}, nil)
	h.probe.internet = func() bool { return h.wifi.aps[1].InUse == false } // 5 GHz AP has no uplink
	h.tick(0)
	h.tick(30 * time.Second)
	want := []string{"home_5G@" + good5.BSSID, "home@" + weak24.BSSID}
	if len(h.wifi.connects) != 2 || h.wifi.connects[0] != want[0] || h.wifi.connects[1] != want[1] {
		t.Fatalf("expected switch then rollback %v, got %v", want, h.wifi.connects)
	}
	if _, why := h.eligible(good5); why == "" {
		t.Error("broken AP should be blacklisted")
	}
}

func TestDeadExitNodeFailsOverAndRestores(t *testing.T) {
	h := newHarness(t, []domain.AP{weak24}, nil)
	h.exits.current = &domain.ExitNode{Name: "exit-1", Online: false}
	h.probe.internet = func() bool { return h.exits.current == nil }

	h.tick(0)
	if len(h.exits.sets) != 1 || h.exits.sets[0] != "" {
		t.Fatalf("expected exit node cleared, got %v", h.exits.sets)
	}
	if h.state[parkedExitNodeKey] != "exit-1" {
		t.Fatal("parked exit node not persisted")
	}

	h.exits.peerUp = true
	h.probe.internet = func() bool { return true }
	for i := 0; i < 3; i++ {
		h.tick(30 * time.Second)
	}
	if got := h.exits.sets[len(h.exits.sets)-1]; got != "exit-1" {
		t.Fatalf("expected exit node restored, got %v", h.exits.sets)
	}
	if h.state[parkedExitNodeKey] != "" {
		t.Error("parked exit node should be cleared after restore")
	}
}

func TestBrokenDNSIsRepaired(t *testing.T) {
	h := newHarness(t, []domain.AP{weak24}, nil)
	h.probe.dns = false
	h.tick(0)
	if h.dns.repairs != 0 {
		t.Fatal("repaired after a single failure")
	}
	h.tick(30 * time.Second)
	if h.dns.repairs != 1 {
		t.Fatalf("expected one repair, got %d", h.dns.repairs)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	h := newHarness(t, []domain.AP{weak24, good5}, func(c *domain.Config) { c.DryRun = true })
	h.exits.current = &domain.ExitNode{Name: "exit-1", Online: false}
	h.probe.internet = func() bool { return false }
	h.probe.dns = false
	for i := 0; i < 4; i++ {
		h.tick(6 * time.Minute)
	}
	if len(h.wifi.connects)+len(h.exits.sets)+h.dns.repairs != 0 {
		t.Fatalf("dry run changed state: connects=%v sets=%v repairs=%d", h.wifi.connects, h.exits.sets, h.dns.repairs)
	}
}
