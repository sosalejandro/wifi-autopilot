// Package service is the autopilot use case: keep configured networks provisioned, fail over a
// dead Tailscale exit node, heal broken DNS, and roam to the AP with the best expected throughput.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
	"github.com/sosalejandro/wifi-autopilot/internal/core/ports"
)

const parkedExitNodeKey = "parked-exit-node"

// Deps are the adapters the autopilot drives.
type Deps struct {
	WiFi      ports.WiFi
	ExitNodes ports.ExitNodes
	Prober    ports.Prober
	DNS       ports.DNSRepairer
	Config    ports.ConfigSource
	State     ports.StateStore
	Log       *slog.Logger
	Now       func() time.Time                           // defaults to time.Now
	Sleep     func(context.Context, time.Duration) error // defaults to a real, cancellable sleep
}

type Autopilot struct {
	Deps
	cfg         *domain.Config
	forceDryRun bool

	saved      map[string]string // SSID -> profile
	lastRescan time.Time
	lastSwitch time.Time
	streakTo   string
	streak     int
	linkFails  int
	dnsFails   int
	blacklist  map[string]time.Time // BSSID -> until

	parkedExitNode string // exit node we turned off because it was dead
	restoreStreak  int
}

func New(ctx context.Context, deps Deps, forceDryRun bool) (*Autopilot, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Sleep == nil {
		deps.Sleep = sleep
	}
	a := &Autopilot{Deps: deps, forceDryRun: forceDryRun, blacklist: map[string]time.Time{}}
	if err := a.loadConfig(ctx); err != nil {
		return nil, err
	}
	a.parkedExitNode = a.State.Get(parkedExitNodeKey)
	return a, nil
}

func (a *Autopilot) loadConfig(ctx context.Context) error {
	cfg, err := a.Config.Load(ctx)
	if err != nil {
		return err
	}
	cfg.DryRun = cfg.DryRun || a.forceDryRun
	a.cfg, a.saved = cfg, nil
	return nil
}

// Run polls until ctx is cancelled.
func (a *Autopilot) Run(ctx context.Context) error {
	a.Log.Info("starting", "interface", a.cfg.Interface, "networks", len(a.cfg.Networks), "poll", a.cfg.PollInterval, "dry_run", a.cfg.DryRun)
	a.syncProfiles(ctx)
	for {
		a.Tick(ctx)
		if err := a.Sleep(ctx, a.cfg.PollInterval); err != nil {
			return nil
		}
	}
}

// Tick runs one poll cycle.
func (a *Autopilot) Tick(ctx context.Context) {
	a.reloadIfChanged(ctx)
	st, err := a.WiFi.State(ctx)
	if err != nil {
		a.Log.Error("reading device state", "err", err)
		return
	}

	online := a.Prober.Internet(ctx, a.cfg.Connectivity)
	if a.handleExitNode(ctx, online) {
		online = a.Prober.Internet(ctx, a.cfg.Connectivity)
	}
	if online {
		a.linkFails = 0
		a.checkDNS(ctx)
	} else {
		a.linkFails++
		a.Log.Warn("no internet (TCP probes failing)", "consecutive", a.linkFails, "wifi", st.WifiConnection)
	}

	if st.WiredUp && a.cfg.SkipWhenWired {
		return
	}
	if err := a.roam(ctx, st); err != nil {
		a.Log.Error("roaming", "err", err)
	}
}

// reloadIfChanged picks up `sops edit` changes without a restart.
func (a *Autopilot) reloadIfChanged(ctx context.Context) {
	if !a.Config.Changed() {
		return
	}
	if err := a.loadConfig(ctx); err != nil {
		a.Log.Error("config changed but is invalid, keeping the previous one", "err", err)
		return
	}
	a.Log.Info("config reloaded", "networks", len(a.cfg.Networks))
	a.syncProfiles(ctx)
}

func (a *Autopilot) syncProfiles(ctx context.Context) {
	managed := a.cfg.ManagedNetworks()
	if a.cfg.DryRun {
		a.Log.Info("would sync network profiles (dry run)", "managed", len(managed))
		return
	}
	changed, err := a.WiFi.SyncProfiles(ctx, managed)
	if err != nil {
		a.Log.Error("syncing network profiles", "err", err)
		return
	}
	if changed > 0 {
		a.Log.Info("network profiles updated", "managed", len(managed), "changed", changed)
		a.saved = nil
	}
}

// handleExitNode turns off a dead exit node and restores it once it comes back.
// It reports whether routing changed.
func (a *Autopilot) handleExitNode(ctx context.Context, online bool) bool {
	if !a.cfg.ExitNode.Failover {
		return false
	}
	en, err := a.ExitNodes.Current(ctx)
	if err != nil {
		a.Log.Debug("exit node status unavailable", "err", err)
		return false
	}

	if en != nil {
		if a.parkedExitNode != "" && a.parkedExitNode != en.Name {
			a.setParked("") // someone picked a different exit node by hand
		}
		dead := !en.Online || a.linkFails+1 >= a.cfg.Switch.LinkFailuresBeforeRoam
		if online || !dead {
			return false
		}
		a.Log.Warn("exit node is not passing traffic, routing directly", "exit_node", en.Name, "online", en.Online)
		if a.cfg.DryRun {
			return false
		}
		if err := a.ExitNodes.Set(ctx, ""); err != nil {
			a.Log.Error("clearing exit node", "err", err)
			return false
		}
		a.setParked(en.Name)
		a.restoreStreak = 0
		return true
	}

	if a.parkedExitNode == "" || !a.cfg.ExitNode.Restore {
		return false
	}
	if up, err := a.ExitNodes.PeerOnline(ctx, a.parkedExitNode); err != nil || !up {
		a.restoreStreak = 0
		return false
	}
	if a.restoreStreak++; a.restoreStreak < a.cfg.ExitNode.RestoreAfterPolls {
		return false
	}
	a.restoreStreak = 0
	a.Log.Info("exit node is back online, restoring it", "exit_node", a.parkedExitNode)
	if a.cfg.DryRun {
		return false
	}
	if err := a.ExitNodes.Set(ctx, a.parkedExitNode); err != nil {
		a.Log.Error("restoring exit node", "err", err)
		return false
	}
	if a.waitOnline(ctx) {
		a.setParked("")
		return true
	}
	a.Log.Warn("restored exit node passes no traffic, routing directly again", "exit_node", a.parkedExitNode)
	if err := a.ExitNodes.Set(ctx, ""); err != nil {
		a.Log.Error("clearing exit node", "err", err)
	}
	return true
}

func (a *Autopilot) checkDNS(ctx context.Context) {
	if a.Prober.DNS(ctx, a.cfg.Connectivity) {
		a.dnsFails = 0
		return
	}
	a.dnsFails++
	a.Log.Warn("DNS lookups failing while the internet is reachable", "consecutive", a.dnsFails)
	if !a.cfg.DNSHeal.Enabled || a.dnsFails < a.cfg.DNSHeal.FailuresBeforeHeal {
		return
	}
	if a.cfg.DryRun {
		a.Log.Info("would repair DNS (dry run)")
		return
	}
	ok := func(ctx context.Context) bool { return a.Prober.DNS(ctx, a.cfg.Connectivity) }
	if err := a.DNS.Repair(ctx, a.cfg.DNSHeal.HeavyCooldown, ok); err != nil {
		a.Log.Error("DNS repair", "err", err)
		return
	}
	a.dnsFails = 0
}

func (a *Autopilot) roam(ctx context.Context, st domain.DeviceState) error {
	now := a.Now()
	rescan := !st.WifiConnected || a.linkFails > 0 || now.Sub(a.lastRescan) >= a.cfg.RescanInterval
	if err := a.refreshSaved(ctx, rescan); err != nil {
		return err
	}
	aps, err := a.WiFi.Scan(ctx, rescan)
	if err != nil {
		return fmt.Errorf("scanning: %w", err)
	}
	if rescan {
		a.lastRescan = now
	}

	var cur *domain.Estimate
	for _, ap := range aps {
		if ap.InUse {
			e := domain.Score(ap, aps, a.weightOf(ap.SSID))
			cur = &e
			break
		}
	}

	force := ""
	switch {
	case !st.WifiConnected || cur == nil:
		force = "not connected"
	case a.linkFails >= a.cfg.Switch.LinkFailuresBeforeRoam:
		a.Log.Warn("blacklisting access point with no internet", "ssid", cur.AP.SSID, "bssid", cur.AP.BSSID, "for", a.cfg.Switch.BlacklistFor)
		a.blacklist[cur.AP.BSSID] = now.Add(a.cfg.Switch.BlacklistFor)
		a.linkFails = 0
		force = "no internet via current access point"
	}

	ranked := domain.Rank(aps, func(ap domain.AP) (float64, bool) {
		w, why := a.eligible(ap)
		return w, why == ""
	})
	if len(ranked) == 0 {
		if force != "" {
			a.Log.Warn("no eligible network in range", "reason", force)
		}
		return nil
	}
	best := ranked[0]

	reason := force
	if reason == "" {
		reason = a.decide(*cur, best)
	}
	if reason != "" {
		a.switchTo(ctx, cur, best, reason)
	}
	return nil
}

func (a *Autopilot) refreshSaved(ctx context.Context, force bool) error {
	if !force && a.saved != nil {
		return nil
	}
	saved, err := a.WiFi.SavedNetworks(ctx)
	if err != nil {
		return fmt.Errorf("listing saved networks: %w", err)
	}
	a.saved = saved
	return nil
}

// decide applies hysteresis so small or momentary differences don't cause flapping.
func (a *Autopilot) decide(cur, best domain.Estimate) string {
	sw := a.cfg.Switch
	switch {
	case best.AP.BSSID == cur.AP.BSSID:
		a.streak = 0
		return ""
	case a.Now().Sub(a.lastSwitch) < sw.Cooldown:
		return ""
	case cur.AP.DBm() < sw.RoamBelowDBm && best.Mbps > cur.Mbps:
		return fmt.Sprintf("current signal weak (%d dBm)", cur.AP.DBm())
	}
	gain := best.Mbps - cur.Mbps
	if best.Mbps < cur.Mbps*(1+sw.MinGainRatio) || gain < sw.MinGainMbps {
		a.streak = 0
		return ""
	}
	if a.streakTo != best.AP.BSSID {
		a.streakTo, a.streak = best.AP.BSSID, 0
	}
	a.streak++
	if a.streak < sw.ConfirmPolls {
		a.Log.Debug("better access point seen, confirming", "ssid", best.AP.SSID, "bssid", best.AP.BSSID, "gain_mbps", math.Round(gain), "streak", a.streak)
		return ""
	}
	return fmt.Sprintf("~%.0f Mbps faster", gain)
}

func (a *Autopilot) switchTo(ctx context.Context, cur *domain.Estimate, best domain.Estimate, reason string) {
	attrs := []any{"reason", reason, "to", best.AP.SSID, "bssid", best.AP.BSSID, "band", best.AP.Band().String(),
		"dbm", best.AP.DBm(), "est_mbps", math.Round(best.Mbps)}
	if cur != nil {
		attrs = append(attrs, "from", cur.AP.SSID, "from_bssid", cur.AP.BSSID, "from_est_mbps", math.Round(cur.Mbps))
	}
	a.lastSwitch, a.streak = a.Now(), 0
	if a.cfg.DryRun {
		a.Log.Info("would switch (dry run)", attrs...)
		return
	}
	a.Log.Info("switching", attrs...)

	err := a.WiFi.Connect(ctx, a.saved[best.AP.SSID], best.AP.BSSID)
	if err == nil && a.waitOnline(ctx) {
		a.Log.Info("switched", "ssid", best.AP.SSID, "bssid", best.AP.BSSID)
		a.linkFails = 0
		return
	}
	a.Log.Warn("new access point has no internet, blacklisting it", "ssid", best.AP.SSID, "bssid", best.AP.BSSID, "err", err)
	a.blacklist[best.AP.BSSID] = a.Now().Add(a.cfg.Switch.BlacklistFor)
	if cur == nil {
		return
	}
	if profile, ok := a.saved[cur.AP.SSID]; ok {
		if err := a.WiFi.Connect(ctx, profile, cur.AP.BSSID); err != nil {
			a.Log.Error("rolling back to previous access point", "ssid", cur.AP.SSID, "err", err)
		}
	}
}

func (a *Autopilot) waitOnline(ctx context.Context) bool {
	deadline := a.Now().Add(a.cfg.Switch.OnlineTimeout)
	for a.Now().Before(deadline) {
		if a.Prober.Internet(ctx, a.cfg.Connectivity) {
			return true
		}
		if err := a.Sleep(ctx, 2*time.Second); err != nil {
			return false
		}
	}
	return false
}

// eligible returns the AP's weight, or why it can't be used.
func (a *Autopilot) eligible(ap domain.AP) (float64, string) {
	if ap.SSID == "" {
		return 0, "hidden"
	}
	if _, ok := a.saved[ap.SSID]; !ok {
		return 0, "no saved profile"
	}
	n, listed := a.cfg.Lookup(ap.SSID)
	if !listed && !a.cfg.UseAllSaved {
		return 0, "not in config"
	}
	if until, ok := a.blacklist[ap.BSSID]; ok {
		if a.Now().Before(until) {
			return 0, "blacklisted " + until.Sub(a.Now()).Round(time.Second).String()
		}
		delete(a.blacklist, ap.BSSID)
	}
	if ap.DBm() < a.cfg.MinDBm {
		return 0, "too weak"
	}
	if !listed {
		return 1, ""
	}
	return n.Weight, ""
}

func (a *Autopilot) weightOf(ssid string) float64 {
	if n, ok := a.cfg.Lookup(ssid); ok {
		return n.Weight
	}
	return 1
}

func (a *Autopilot) setParked(name string) {
	a.parkedExitNode = name
	if err := a.State.Set(parkedExitNodeKey, name); err != nil {
		a.Log.Warn("persisting parked exit node", "err", err)
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
