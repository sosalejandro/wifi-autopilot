package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config is the autopilot's validated configuration.
type Config struct {
	Interface      string
	PollInterval   time.Duration
	RescanInterval time.Duration
	MinDBm         int
	SkipWhenWired  bool
	UseAllSaved    bool
	DryRun         bool
	Networks       []Network
	Switch         SwitchPolicy
	Connectivity   Connectivity
	DNSHeal        DNSHealPolicy
	ExitNode       ExitNodePolicy
}

// Network is an allowed Wi-Fi network. With a password (or Security "open") the autopilot
// provisions the profile itself; otherwise it reuses one already saved on the machine.
type Network struct {
	SSID     string
	Password string
	Security string  // wpa-psk (default when a password is set), sae, open
	Weight   float64 // preference multiplier, default 1
}

func (n Network) Managed() bool { return n.Password != "" || n.Security == "open" }

// SwitchPolicy is the hysteresis that keeps the autopilot from flapping between APs.
type SwitchPolicy struct {
	MinGainRatio           float64
	MinGainMbps            float64
	ConfirmPolls           int
	Cooldown               time.Duration
	RoamBelowDBm           int
	BlacklistFor           time.Duration
	LinkFailuresBeforeRoam int
	OnlineTimeout          time.Duration
}

type Connectivity struct {
	TCPTargets []string
	DNSNames   []string
	Timeout    time.Duration
}

type DNSHealPolicy struct {
	Enabled            bool
	FailuresBeforeHeal int
	HeavyCooldown      time.Duration
}

// ExitNodePolicy controls failover when the Tailscale exit node stops passing traffic.
type ExitNodePolicy struct {
	Failover          bool
	Restore           bool
	RestoreAfterPolls int
}

func DefaultConfig() Config {
	return Config{
		Interface:      "wlan0",
		PollInterval:   30 * time.Second,
		RescanInterval: 2 * time.Minute,
		MinDBm:         -82,
		SkipWhenWired:  true,
		Switch: SwitchPolicy{
			MinGainRatio:           0.25,
			MinGainMbps:            30,
			ConfirmPolls:           2,
			Cooldown:               5 * time.Minute,
			RoamBelowDBm:           -75,
			BlacklistFor:           10 * time.Minute,
			LinkFailuresBeforeRoam: 3,
			OnlineTimeout:          25 * time.Second,
		},
		Connectivity: Connectivity{
			TCPTargets: []string{"1.1.1.1:443", "8.8.8.8:443"},
			DNSNames:   []string{"google.com", "cloudflare.com"},
			Timeout:    5 * time.Second,
		},
		DNSHeal:  DNSHealPolicy{Enabled: true, FailuresBeforeHeal: 2, HeavyCooldown: 15 * time.Minute},
		ExitNode: ExitNodePolicy{Failover: true, Restore: true, RestoreAfterPolls: 3},
	}
}

// Normalize fills per-network defaults.
func (c *Config) Normalize() {
	for i := range c.Networks {
		n := &c.Networks[i]
		if n.Weight == 0 {
			n.Weight = 1
		}
		if n.Security == "" && n.Password != "" {
			n.Security = "wpa-psk"
		}
	}
}

func (c *Config) Validate() error {
	var errs []error
	if c.Interface == "" {
		errs = append(errs, errors.New("interface is required"))
	}
	if c.PollInterval < 5*time.Second {
		errs = append(errs, errors.New("poll_interval must be at least 5s"))
	}
	if len(c.Networks) == 0 && !c.UseAllSaved {
		errs = append(errs, errors.New("no networks listed and use_all_saved is false"))
	}
	seen := map[string]bool{}
	for _, n := range c.Networks {
		switch {
		case n.SSID == "":
			errs = append(errs, errors.New("network with empty ssid"))
		case strings.ContainsAny(n.SSID, ";\n\r") || len(n.SSID) > 32:
			errs = append(errs, fmt.Errorf("ssid %q: unsupported characters or longer than 32 bytes", n.SSID))
		case seen[n.SSID]:
			errs = append(errs, fmt.Errorf("ssid %q listed twice", n.SSID))
		}
		seen[n.SSID] = true
		switch n.Security {
		case "", "open":
		case "wpa-psk", "sae":
			if n.Password == "" {
				errs = append(errs, fmt.Errorf("ssid %q: security %s needs a password", n.SSID, n.Security))
			} else if len(n.Password) < 8 || len(n.Password) > 64 || strings.ContainsAny(n.Password, "\n\r") {
				errs = append(errs, fmt.Errorf("ssid %q: password must be 8-64 characters", n.SSID))
			}
		default:
			errs = append(errs, fmt.Errorf("ssid %q: unknown security %q (want wpa-psk, sae or open)", n.SSID, n.Security))
		}
		if n.Weight < 0 {
			errs = append(errs, fmt.Errorf("ssid %q: weight must be positive", n.SSID))
		}
	}
	if len(c.Connectivity.TCPTargets) == 0 || len(c.Connectivity.DNSNames) == 0 {
		errs = append(errs, errors.New("connectivity needs tcp_targets and dns_names"))
	}
	return errors.Join(errs...)
}

// Lookup returns the configured entry for an SSID.
func (c *Config) Lookup(ssid string) (Network, bool) {
	for _, n := range c.Networks {
		if n.SSID == ssid {
			return n, true
		}
	}
	return Network{}, false
}

// ManagedNetworks lists networks whose credentials come from the config.
func (c *Config) ManagedNetworks() []Network {
	var out []Network
	for _, n := range c.Networks {
		if n.Managed() {
			out = append(out, n)
		}
	}
	return out
}
