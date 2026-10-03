// Command wifi-autopilot keeps a Linux laptop on the best available Wi-Fi network,
// fails over a dead Tailscale exit node, and repairs broken DNS.
//
// This is the composition root: it is the only place that knows about concrete adapters.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/sosalejandro/wifi-autopilot/internal/adapters/filestate"
	"github.com/sosalejandro/wifi-autopilot/internal/adapters/netprobe"
	"github.com/sosalejandro/wifi-autopilot/internal/adapters/networkmanager"
	"github.com/sosalejandro/wifi-autopilot/internal/adapters/resolved"
	"github.com/sosalejandro/wifi-autopilot/internal/adapters/sopsconfig"
	"github.com/sosalejandro/wifi-autopilot/internal/adapters/tailscale"
	"github.com/sosalejandro/wifi-autopilot/internal/core/service"
)

func main() {
	cfgPath := flag.String("config", "/etc/wifi-autopilot/config.sops.yaml", "config file (plain or sops-encrypted YAML)")
	stateDir := flag.String("state-dir", "/var/lib/wifi-autopilot", "where small runtime state is persisted")
	once := flag.Bool("once", false, "print the current ranking and connectivity, then exit")
	dryRun := flag.Bool("dry-run", false, "log decisions without switching networks, writing profiles or repairing DNS")
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	log := newLogger(*debug)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, *cfgPath, *stateDir, *once, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "wifi-autopilot:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, cfgPath, stateDir string, once, dryRun bool) error {
	cfgSource := sopsconfig.New(cfgPath)
	// The interface is fixed for the process lifetime; peek at it before wiring the Wi-Fi adapter.
	cfg, err := cfgSource.Load(ctx)
	if err != nil {
		return err
	}

	ap, err := service.New(ctx, service.Deps{
		WiFi:      networkmanager.New(cfg.Interface),
		ExitNodes: tailscale.New(),
		Prober:    netprobe.Prober{},
		DNS:       resolved.New(log.With("component", "dns-repair")),
		Config:    cfgSource,
		State:     filestate.Store{Dir: stateDir},
		Log:       log,
	}, dryRun || once)
	if err != nil {
		return err
	}

	if once {
		r, err := ap.Snapshot(ctx)
		if err != nil {
			return err
		}
		printReport(os.Stdout, r)
		return nil
	}
	return ap.Run(ctx)
}

func newLogger(debug bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if debug {
		opts.Level = slog.LevelDebug
	}
	if os.Getenv("INVOCATION_ID") != "" { // under systemd, journald already timestamps lines
		opts.ReplaceAttr = func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func printReport(w io.Writer, r *service.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\tSSID\tBSSID\tBAND\tCH\tWIDTH\tdBm\tSIGNAL\tCONGEST\tWEIGHT\tEST Mbps\tNOTE")
	for _, row := range r.Rows {
		mark := " "
		if row.AP.InUse {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%.0f%%\t%.2f\t%.2f\t%.0f\t%s\n", mark, row.AP.SSID, row.AP.BSSID,
			row.AP.Band(), row.AP.Channel, row.AP.WidthMHz, row.AP.DBm(), row.SignalEff*100, row.Congestion, row.Weight, row.Mbps, row.Note)
	}
	tw.Flush()
	if len(r.MissingProfiles) > 0 {
		fmt.Fprintf(w, "\nno profile yet (provisioned when the service runs): %v\n", r.MissingProfiles)
	}
	fmt.Fprintf(w, "\ninternet (tcp): %v   dns: %v\n", r.Internet, r.DNS)
	if r.ExitNode != nil {
		fmt.Fprintf(w, "tailscale exit node: %s (online=%v)\n", r.ExitNode.Name, r.ExitNode.Online)
	}
}
