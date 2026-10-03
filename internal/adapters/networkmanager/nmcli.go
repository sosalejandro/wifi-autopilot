// Package networkmanager implements ports.WiFi on top of NetworkManager's nmcli.
package networkmanager

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
	"github.com/sosalejandro/wifi-autopilot/internal/platform/cmdexec"
)

// DefaultProfileDir is where NetworkManager keeps system-wide keyfile profiles.
const DefaultProfileDir = "/etc/NetworkManager/system-connections"

type Adapter struct {
	Iface      string
	ProfileDir string
	Run        cmdexec.Runner
}

func New(iface string) *Adapter {
	return &Adapter{Iface: iface, ProfileDir: DefaultProfileDir, Run: cmdexec.Exec}
}

func (a *Adapter) nmcli(ctx context.Context, args ...string) ([]byte, error) {
	return a.Run(ctx, "nmcli", args...)
}

const scanFields = "SSID,BSSID,CHAN,FREQ,RATE,BANDWIDTH,SIGNAL,SECURITY,IN-USE"

func (a *Adapter) Scan(ctx context.Context, rescan bool) ([]domain.AP, error) {
	r := "no"
	if rescan {
		r = "yes"
	}
	out, err := a.nmcli(ctx, "-t", "-e", "yes", "-f", scanFields, "device", "wifi", "list", "ifname", a.Iface, "--rescan", r)
	if err != nil {
		return nil, err
	}
	return parseScan(out)
}

func parseScan(out []byte) ([]domain.AP, error) {
	var aps []domain.AP
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitTerse(line)
		if len(f) != 9 {
			return nil, fmt.Errorf("unexpected scan line %q", line)
		}
		aps = append(aps, domain.AP{
			SSID:     f[0],
			BSSID:    f[1],
			Channel:  leadingInt(f[2]),
			FreqMHz:  leadingInt(f[3]),
			RateMbps: leadingInt(f[4]),
			WidthMHz: leadingInt(f[5]),
			Signal:   leadingInt(f[6]),
			Security: f[7],
			InUse:    strings.TrimSpace(f[8]) == "*",
		})
	}
	return aps, nil
}

func (a *Adapter) SavedNetworks(ctx context.Context) (map[string]string, error) {
	out, err := a.nmcli(ctx, "-t", "-f", "UUID,TYPE", "connection", "show")
	if err != nil {
		return nil, err
	}
	saved := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := splitTerse(line)
		if len(f) != 2 || f[1] != "802-11-wireless" {
			continue
		}
		v, err := a.nmcli(ctx, "-g", "connection.id,802-11-wireless.ssid", "connection", "show", "uuid", f[0])
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(strings.TrimRight(string(v), "\n"), "\n", 2)
		if len(parts) != 2 {
			continue
		}
		name, ssid := unescape(parts[0]), unescape(parts[1])
		// Prefer the profile wifi-autopilot manages when an SSID has several.
		if _, dup := saved[ssid]; !dup || isManaged(name) {
			saved[ssid] = name
		}
	}
	return saved, nil
}

func (a *Adapter) State(ctx context.Context) (domain.DeviceState, error) {
	out, err := a.nmcli(ctx, "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if err != nil {
		return domain.DeviceState{}, err
	}
	var st domain.DeviceState
	for _, line := range strings.Split(string(out), "\n") {
		f := splitTerse(line)
		if len(f) != 4 {
			continue
		}
		switch {
		case f[0] == a.Iface:
			st.WifiConnected = f[2] == "connected"
			st.WifiConnection = f[3]
		case f[1] == "ethernet" && f[2] == "connected":
			st.WiredUp = true
		}
	}
	return st, nil
}

func (a *Adapter) Connect(ctx context.Context, profile, bssid string) error {
	_, err := a.nmcli(ctx, "--wait", "45", "connection", "up", "id", profile, "ifname", a.Iface, "ap", bssid)
	return err
}

// splitTerse splits one line of `nmcli -t` output, honouring `\:` and `\\` escapes.
func splitTerse(line string) []string {
	var fields []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteByte(line[i])
		case c == ':':
			fields = append(fields, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(fields, b.String())
}

func unescape(s string) string { return strings.Join(splitTerse(s), ":") }

// leadingInt parses "2457 MHz" -> 2457; anything unparseable is 0.
func leadingInt(s string) int {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(f[0])
	return n
}
