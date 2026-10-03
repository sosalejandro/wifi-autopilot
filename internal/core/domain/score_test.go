package domain

import "testing"

func ap(ssid, bssid string, ch, freq, width, signal int) AP {
	return AP{SSID: ssid, BSSID: bssid, Channel: ch, FreqMHz: freq, WidthMHz: width, Signal: signal, RateMbps: 1170}
}

func TestFiveGHzBeatsStrongButCrowded24(t *testing.T) {
	scan := []AP{
		ap("home_5G", "00:00:5E:00:53:95", 153, 5765, 80, 59),
		ap("home", "00:00:5E:00:53:93", 10, 2457, 20, 87),
		ap("n1", "00:00:5E:00:53:52", 7, 2442, 40, 70),
		ap("n2", "02:00:5E:00:53:52", 7, 2442, 40, 67),
		ap("n3", "00:00:5E:00:53:92", 7, 2442, 40, 49),
	}
	r := Rank(scan, func(a AP) (float64, bool) { return 1, a.SSID == "home" || a.SSID == "home_5G" })
	if len(r) != 2 || r[0].AP.SSID != "home_5G" {
		t.Fatalf("expected 5 GHz first, got %+v", r)
	}
	if r[1].Congestion <= 1 {
		t.Errorf("2.4 GHz should see congestion, got %.2f", r[1].Congestion)
	}
}

func TestWeakFiveGHzLosesToStrong24(t *testing.T) {
	r := Rank([]AP{
		ap("home_5G", "AA:00:00:00:00:01", 36, 5180, 80, 15), // ~-91 dBm
		ap("home", "AA:00:00:00:00:02", 1, 2412, 20, 80),
	}, func(AP) (float64, bool) { return 1, true })
	if r[0].AP.SSID != "home" {
		t.Fatalf("expected 2.4 GHz first at long range, got %s (%.0f vs %.0f)", r[0].AP.SSID, r[0].Mbps, r[1].Mbps)
	}
}

func TestVirtualAPOnSameRadioIsNotCongestion(t *testing.T) {
	main := ap("home", "00:00:5E:00:53:93", 10, 2457, 20, 87)
	guest := ap("guest", "02:00:5E:00:53:93", 10, 2457, 20, 89)
	if c := Score(main, []AP{main, guest}, 1).Congestion; c != 1 {
		t.Errorf("congestion %.2f, want 1", c)
	}
}

func TestWeightScales(t *testing.T) {
	a := ap("x", "AA:00:00:00:00:01", 36, 5180, 80, 80)
	if full, half := Score(a, nil, 1).Mbps, Score(a, nil, 0.5).Mbps; half*2-full > 1e-9 {
		t.Errorf("weight not linear: %.1f vs %.1f", full, half)
	}
}

func TestDBm(t *testing.T) {
	for sig, want := range map[int]int{100: -40, 0: -100, 59: -64} {
		if got := (AP{Signal: sig}).DBm(); got != want {
			t.Errorf("signal %d: got %d dBm, want %d", sig, got, want)
		}
	}
}
