// Package ports declares what the autopilot core needs from the outside world.
// Adapters in internal/adapters implement these; the core never imports an adapter.
package ports

import (
	"context"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

// WiFi is the network manager driving the wireless interface.
type WiFi interface {
	State(ctx context.Context) (domain.DeviceState, error)
	// Scan lists visible APs; rescan forces a fresh radio scan instead of using the cache.
	Scan(ctx context.Context, rescan bool) ([]domain.AP, error)
	// SavedNetworks maps SSID to the profile used to join it.
	SavedNetworks(ctx context.Context) (map[string]string, error)
	// Connect joins a saved profile, pinned to one access point.
	Connect(ctx context.Context, profile, bssid string) error
	// SyncProfiles provisions exactly these networks' credentials, returning how many changed.
	SyncProfiles(ctx context.Context, networks []domain.Network) (int, error)
}

// ExitNodes controls the Tailscale exit node.
type ExitNodes interface {
	// Current returns the active exit node, or nil when none is set.
	Current(ctx context.Context) (*domain.ExitNode, error)
	PeerOnline(ctx context.Context, name string) (bool, error)
	// Set switches the exit node; an empty name routes traffic directly.
	Set(ctx context.Context, name string) error
}

// Prober checks connectivity from this machine.
type Prober interface {
	// Internet reports whether any TCP target is reachable by IP.
	Internet(ctx context.Context, c domain.Connectivity) bool
	// DNS reports whether any name resolves through the system resolver.
	DNS(ctx context.Context, c domain.Connectivity) bool
}

// DNSRepairer fixes the system's DNS stack, stopping as soon as ok passes.
// Disruptive steps run at most once per heavyCooldown.
type DNSRepairer interface {
	Repair(ctx context.Context, heavyCooldown time.Duration, ok func(context.Context) bool) error
}

// ConfigSource loads the configuration and reports when it has changed.
type ConfigSource interface {
	Load(ctx context.Context) (*domain.Config, error)
	Changed() bool
}

// StateStore persists small values across restarts.
type StateStore interface {
	Get(key string) string
	Set(key, value string) error
}
