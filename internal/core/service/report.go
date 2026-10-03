package service

import (
	"context"
	"sort"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

// Report is a snapshot of what the autopilot sees, for `-once`.
type Report struct {
	Rows            []ReportRow
	MissingProfiles []string // configured SSIDs with no profile yet
	Internet, DNS   bool
	ExitNode        *domain.ExitNode
}

type ReportRow struct {
	domain.Estimate
	Note string // why the AP is not eligible, empty if it is
}

func (a *Autopilot) Snapshot(ctx context.Context) (*Report, error) {
	if err := a.refreshSaved(ctx, true); err != nil {
		return nil, err
	}
	aps, err := a.WiFi.Scan(ctx, true)
	if err != nil {
		return nil, err
	}

	r := &Report{}
	for _, ap := range aps {
		_, listed := a.cfg.Lookup(ap.SSID)
		if _, saved := a.saved[ap.SSID]; !listed && !saved {
			continue
		}
		w, why := a.eligible(ap)
		if why != "" {
			w = a.weightOf(ap.SSID)
		}
		r.Rows = append(r.Rows, ReportRow{domain.Score(ap, aps, w), why})
	}
	sort.SliceStable(r.Rows, func(i, j int) bool { return r.Rows[i].Mbps > r.Rows[j].Mbps })

	for _, n := range a.cfg.Networks {
		if _, ok := a.saved[n.SSID]; !ok {
			r.MissingProfiles = append(r.MissingProfiles, n.SSID)
		}
	}
	r.Internet = a.Prober.Internet(ctx, a.cfg.Connectivity)
	r.DNS = a.Prober.DNS(ctx, a.cfg.Connectivity)
	if en, err := a.ExitNodes.Current(ctx); err == nil {
		r.ExitNode = en
	}
	return r, nil
}
