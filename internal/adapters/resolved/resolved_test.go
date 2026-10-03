package resolved

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/core/ports"
)

var _ ports.DNSRepairer = (*Repairer)(nil)

func newTest(t *testing.T, units map[string]string) (*Repairer, *[]string) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; the repairer targets Linux")
	}
	var calls []string
	r := &Repairer{
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		ResolvConf: filepath.Join(t.TempDir(), "resolv.conf"),
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			c := name + " " + strings.Join(args, " ")
			calls = append(calls, c)
			return []byte(units[c]), nil
		},
	}
	return r, &calls
}

func TestRepairStopsAtFirstFix(t *testing.T) {
	r, calls := newTest(t, map[string]string{
		"systemctl is-active systemd-resolved":  "inactive",
		"systemctl is-enabled systemd-resolved": "disabled",
	})
	os.WriteFile(r.ResolvConf, []byte("nameserver 8.8.8.8\n"), 0o644)

	fixed := false
	err := r.Repair(context.Background(), time.Hour, func(context.Context) bool { return fixed })
	if err == nil {
		t.Fatal("expected failure: ok never passes")
	}
	if target, _ := os.Readlink(r.ResolvConf); target != stubResolv {
		t.Errorf("resolv.conf not linked to stub, got %q", target)
	}
	if _, err := os.Stat(r.ResolvConf + ".wifi-autopilot.bak"); err != nil {
		t.Error("original resolv.conf not backed up")
	}
	if !contains(*calls, "systemctl enable --now systemd-resolved") || !contains(*calls, "systemctl restart NetworkManager") {
		t.Errorf("missing steps: %v", *calls)
	}

	*calls = nil
	if err := r.Repair(context.Background(), time.Hour, func(context.Context) bool { return false }); err == nil {
		t.Fatal("expected failure")
	}
	if contains(*calls, "systemctl restart NetworkManager") {
		t.Error("heavy step ignored its cooldown")
	}
}

func TestRepairSkipsHeavyStepWhenFlushWorks(t *testing.T) {
	r, calls := newTest(t, map[string]string{
		"systemctl is-active systemd-resolved":  "active",
		"systemctl is-enabled systemd-resolved": "enabled",
	})
	os.Symlink(stubResolv, r.ResolvConf)
	n := 0
	err := r.Repair(context.Background(), time.Hour, func(context.Context) bool { n++; return n >= 3 })
	if err != nil {
		t.Fatal(err)
	}
	if contains(*calls, "systemctl restart NetworkManager") {
		t.Errorf("restarted services unnecessarily: %v", *calls)
	}
}

func contains(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}
