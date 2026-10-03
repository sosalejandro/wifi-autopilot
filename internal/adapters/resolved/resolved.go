// Package resolved implements ports.DNSRepairer for systemd-resolved hosts. The fix it applies:
// systemd-resolved owns /etc/resolv.conf, so NetworkManager and Tailscale (MagicDNS, exit nodes)
// both push their resolvers into it instead of fighting over the file.
package resolved

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/platform/cmdexec"
)

const (
	DefaultResolvConf = "/etc/resolv.conf"
	stubResolv        = "/run/systemd/resolve/stub-resolv.conf"
)

type Repairer struct {
	Log        *slog.Logger
	Run        cmdexec.Runner
	ResolvConf string
	Settle     time.Duration // wait after restarting a service

	lastHeavy time.Time
}

func New(log *slog.Logger) *Repairer {
	return &Repairer{Log: log, Run: cmdexec.Exec, ResolvConf: DefaultResolvConf, Settle: 10 * time.Second}
}

type step struct {
	name  string
	heavy bool // disruptive: drops the link for a few seconds
	fn    func(context.Context) error
}

// Repair runs steps from least to most disruptive, stopping as soon as ok passes.
func (r *Repairer) Repair(ctx context.Context, heavyCooldown time.Duration, ok func(context.Context) bool) error {
	steps := []step{
		{"enable systemd-resolved", false, r.ensureResolved},
		{"point /etc/resolv.conf at the resolved stub", false, r.ensureStubSymlink},
		{"flush DNS caches", false, func(ctx context.Context) error { return r.cmd(ctx, "resolvectl", "flush-caches") }},
		{"restart NetworkManager and tailscaled", true, r.restartStack},
	}
	for _, s := range steps {
		if s.heavy && !r.lastHeavy.IsZero() && time.Since(r.lastHeavy) < heavyCooldown {
			r.Log.Warn("skipping step, cooling down", "step", s.name, "since_last", time.Since(r.lastHeavy).Round(time.Second))
			break
		}
		r.Log.Info("running step", "step", s.name)
		if s.heavy {
			r.lastHeavy = time.Now()
		}
		if err := s.fn(ctx); err != nil {
			r.Log.Error("step failed", "step", s.name, "err", err)
			continue
		}
		if ok(ctx) {
			r.Log.Info("DNS repaired", "step", s.name)
			return nil
		}
	}
	return errors.New("DNS still failing after all repair steps")
}

func (r *Repairer) ensureResolved(ctx context.Context) error {
	if r.unitIs(ctx, "systemd-resolved", "is-active", "active") && r.unitIs(ctx, "systemd-resolved", "is-enabled", "enabled") {
		return nil
	}
	if err := r.cmd(ctx, "systemctl", "enable", "--now", "systemd-resolved"); err != nil {
		return err
	}
	return sleep(ctx, r.Settle)
}

func (r *Repairer) ensureStubSymlink(context.Context) error {
	if target, err := os.Readlink(r.ResolvConf); err == nil && target == stubResolv {
		return nil
	}
	if info, err := os.Lstat(r.ResolvConf); err == nil && info.Mode().IsRegular() {
		bak := r.ResolvConf + ".wifi-autopilot.bak"
		r.Log.Info("backing up resolv.conf", "to", bak)
		if err := os.Rename(r.ResolvConf, bak); err != nil {
			return err
		}
	} else if err := os.Remove(r.ResolvConf); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(stubResolv, r.ResolvConf)
}

// restartStack makes NetworkManager re-detect the resolved stub and Tailscale re-register its DNS.
func (r *Repairer) restartStack(ctx context.Context) error {
	if err := r.cmd(ctx, "systemctl", "restart", "NetworkManager"); err != nil {
		return err
	}
	if err := sleep(ctx, r.Settle); err != nil {
		return err
	}
	if r.unitIs(ctx, "tailscaled", "is-active", "active") {
		if err := r.cmd(ctx, "systemctl", "restart", "tailscaled"); err != nil {
			return err
		}
	}
	return sleep(ctx, r.Settle)
}

func (r *Repairer) unitIs(ctx context.Context, unit, verb, want string) bool {
	out, _ := r.Run(ctx, "systemctl", verb, unit)
	return strings.TrimSpace(string(out)) == want
}

func (r *Repairer) cmd(ctx context.Context, name string, args ...string) error {
	_, err := r.Run(ctx, name, args...)
	return err
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
