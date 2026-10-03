package domain

import "sort"

// macEfficiency is the typical ratio of real TCP throughput to PHY rate.
const macEfficiency = 0.65

// Estimate is an AP's expected usable throughput, broken down so decisions can be explained.
// It is computed from scan data alone, so candidates are compared without disconnecting.
type Estimate struct {
	AP         AP
	PhyMbps    float64 // best PHY rate this client can negotiate with the AP
	SignalEff  float64 // 0-1, fraction of the PHY rate reachable at this signal level
	Congestion float64 // >= 1, airtime divisor from overlapping networks
	Weight     float64 // user preference multiplier
	Mbps       float64 // expected usable throughput
}

// Score estimates one AP against everything else in the same scan.
func Score(ap AP, scan []AP, weight float64) Estimate {
	e := Estimate{
		AP:         ap,
		PhyMbps:    phyMbps(ap),
		SignalEff:  signalEfficiency(ap.DBm()),
		Congestion: congestion(ap, scan),
		Weight:     weight,
	}
	e.Mbps = e.PhyMbps * macEfficiency * e.SignalEff / e.Congestion * weight
	return e
}

// Rank scores every AP that weight accepts, best first.
func Rank(scan []AP, weight func(AP) (float64, bool)) []Estimate {
	var out []Estimate
	for _, ap := range scan {
		if w, ok := weight(ap); ok {
			out = append(out, Score(ap, scan, w))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mbps > out[j].Mbps })
	return out
}

// phyMbps models a 2x2 Wi-Fi 5 client such as the Intel 9560: 802.11n on 2.4 GHz,
// 802.11ac on 5 GHz. Capped by the AP's advertised max rate.
func phyMbps(ap AP) float64 {
	w := ap.WidthMHz
	var phy float64
	switch {
	case ap.Band() == Band2G && w >= 40:
		phy = 300
	case ap.Band() == Band2G:
		phy = 144
	case w >= 160:
		phy = 1733
	case w >= 80 || w == 0:
		phy = 867
	case w >= 40:
		phy = 400
	default:
		phy = 173
	}
	if ap.RateMbps > 0 && float64(ap.RateMbps) < phy {
		phy = float64(ap.RateMbps)
	}
	return phy
}

// signalCurve approximates the MCS a 2x2 client sustains at a given RSSI.
var signalCurve = []struct{ dbm, eff float64 }{
	{-50, 1.0}, {-60, 0.85}, {-67, 0.6}, {-72, 0.4}, {-78, 0.18}, {-84, 0.04}, {-88, 0},
}

func signalEfficiency(dbm int) float64 {
	d := float64(dbm)
	if d >= signalCurve[0].dbm {
		return signalCurve[0].eff
	}
	for i := 1; i < len(signalCurve); i++ {
		hi, lo := signalCurve[i-1], signalCurve[i]
		if d >= lo.dbm {
			return lo.eff + (d-lo.dbm)/(hi.dbm-lo.dbm)*(hi.eff-lo.eff)
		}
	}
	return 0
}

// congestion grows with the number and strength of other networks sharing the channel.
func congestion(ap AP, scan []AP) float64 {
	load := 0.0
	for _, o := range scan {
		if o.BSSID == ap.BSSID || o.Band() != ap.Band() || sameRadio(ap, o) || !overlaps(ap, o) {
			continue
		}
		load += min(max(float64(o.Signal-20)/80, 0), 1)
	}
	return 1 + 0.3*load
}

// sameRadio catches virtual APs (guest/hidden SSIDs) broadcast by one physical radio,
// which share a channel and the trailing BSSID octets.
func sameRadio(a, b AP) bool {
	n, m := len(a.BSSID), len(b.BSSID)
	return a.Channel == b.Channel && n >= 5 && m >= 5 && a.BSSID[n-5:] == b.BSSID[m-5:]
}

func overlaps(a, b AP) bool {
	gap := abs(a.FreqMHz - b.FreqMHz)
	if a.Band() == Band2G {
		return gap < 25 // 2.4 GHz channels are 5 MHz apart and 20+ MHz wide
	}
	return gap < max(a.WidthMHz, b.WidthMHz, 20)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
