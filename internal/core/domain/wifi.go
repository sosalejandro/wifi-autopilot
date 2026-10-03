// Package domain holds wifi-autopilot's core types and rules. It has no I/O and no
// dependencies outside the standard library.
package domain

// Band is a Wi-Fi frequency band.
type Band int

const (
	BandUnknown Band = iota
	Band2G
	Band5G
	Band6G
)

func (b Band) String() string {
	switch b {
	case Band2G:
		return "2.4GHz"
	case Band5G:
		return "5GHz"
	case Band6G:
		return "6GHz"
	}
	return "?"
}

// BandOf maps a frequency to its band.
func BandOf(freqMHz int) Band {
	switch {
	case freqMHz >= 2400 && freqMHz < 2500:
		return Band2G
	case freqMHz >= 5150 && freqMHz < 5925:
		return Band5G
	case freqMHz >= 5925 && freqMHz < 7125:
		return Band6G
	}
	return BandUnknown
}

// AP is one access point (BSSID) seen in a scan.
type AP struct {
	SSID     string
	BSSID    string
	Channel  int
	FreqMHz  int
	RateMbps int // max rate advertised by the AP
	WidthMHz int // channel width
	Signal   int // quality 0-100, as NetworkManager reports it
	Security string
	InUse    bool
}

func (a AP) Band() Band { return BandOf(a.FreqMHz) }

// DBm converts the 0-100 quality back to dBm (NetworkManager maps -100 dBm => 0, -40 dBm => 100).
func (a AP) DBm() int { return -40 - (100-a.Signal)*60/100 }

// DeviceState is the link state the autopilot cares about.
type DeviceState struct {
	WifiConnected  bool
	WifiConnection string
	WiredUp        bool
}

// ExitNode is the Tailscale exit node currently routing all traffic.
type ExitNode struct {
	Name   string
	Online bool
}
