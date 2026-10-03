// Package sopsconfig implements ports.ConfigSource: a YAML file that is decrypted with
// `sops -d` when it carries sops metadata (age keys via SOPS_AGE_KEY_FILE), or read as-is.
package sopsconfig

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

type Source struct {
	Path string
	// Decrypt turns an encrypted file into plaintext YAML; defaults to the sops CLI.
	Decrypt func(ctx context.Context, path string) ([]byte, error)

	modTime time.Time
}

func New(path string) *Source { return &Source{Path: path, Decrypt: sopsDecrypt} }

// Changed reports whether the file was modified since the last Load attempt.
func (s *Source) Changed() bool {
	info, err := os.Stat(s.Path)
	return err == nil && !info.ModTime().Equal(s.modTime)
}

func (s *Source) Load(ctx context.Context) (*domain.Config, error) {
	info, err := os.Stat(s.Path)
	if err != nil {
		return nil, err
	}
	s.modTime = info.ModTime()
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	if isEncrypted(raw) {
		if raw, err = s.Decrypt(ctx, s.Path); err != nil {
			return nil, err
		}
	}
	cfg, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	return cfg, nil
}

func isEncrypted(raw []byte) bool {
	var probe struct {
		Sops any `yaml:"sops"`
	}
	return yaml.Unmarshal(raw, &probe) == nil && probe.Sops != nil
}

func sopsDecrypt(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sops", "--decrypt", "--input-type", "yaml", "--output-type", "yaml", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// file mirrors the YAML layout; pointers distinguish "unset" from zero so defaults survive.
type file struct {
	Interface      *string        `yaml:"interface"`
	PollInterval   *time.Duration `yaml:"poll_interval"`
	RescanInterval *time.Duration `yaml:"rescan_interval"`
	MinDBm         *int           `yaml:"min_dbm"`
	SkipWhenWired  *bool          `yaml:"skip_when_wired"`
	UseAllSaved    *bool          `yaml:"use_all_saved"`
	DryRun         *bool          `yaml:"dry_run"`
	Networks       []struct {
		SSID     string  `yaml:"ssid"`
		Password string  `yaml:"password"`
		Security string  `yaml:"security"`
		Weight   float64 `yaml:"weight"`
	} `yaml:"networks"`
	Switch struct {
		MinGainRatio           *float64       `yaml:"min_gain_ratio"`
		MinGainMbps            *float64       `yaml:"min_gain_mbps"`
		ConfirmPolls           *int           `yaml:"confirm_polls"`
		Cooldown               *time.Duration `yaml:"cooldown"`
		RoamBelowDBm           *int           `yaml:"roam_below_dbm"`
		BlacklistFor           *time.Duration `yaml:"blacklist_for"`
		LinkFailuresBeforeRoam *int           `yaml:"link_failures_before_roam"`
		OnlineTimeout          *time.Duration `yaml:"online_timeout"`
	} `yaml:"switch"`
	Connectivity struct {
		TCPTargets []string       `yaml:"tcp_targets"`
		DNSNames   []string       `yaml:"dns_names"`
		Timeout    *time.Duration `yaml:"timeout"`
	} `yaml:"connectivity"`
	DNSHeal struct {
		Enabled            *bool          `yaml:"enabled"`
		FailuresBeforeHeal *int           `yaml:"failures_before_heal"`
		HeavyCooldown      *time.Duration `yaml:"heavy_cooldown"`
	} `yaml:"dns_heal"`
	ExitNode struct {
		Failover          *bool `yaml:"failover"`
		Restore           *bool `yaml:"restore"`
		RestoreAfterPolls *int  `yaml:"restore_after_polls"`
	} `yaml:"exit_node"`
}

func parse(raw []byte) (*domain.Config, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	c := domain.DefaultConfig()
	set(&c.Interface, f.Interface)
	set(&c.PollInterval, f.PollInterval)
	set(&c.RescanInterval, f.RescanInterval)
	set(&c.MinDBm, f.MinDBm)
	set(&c.SkipWhenWired, f.SkipWhenWired)
	set(&c.UseAllSaved, f.UseAllSaved)
	set(&c.DryRun, f.DryRun)
	for _, n := range f.Networks {
		c.Networks = append(c.Networks, domain.Network{SSID: n.SSID, Password: n.Password, Security: n.Security, Weight: n.Weight})
	}
	sw := &c.Switch
	set(&sw.MinGainRatio, f.Switch.MinGainRatio)
	set(&sw.MinGainMbps, f.Switch.MinGainMbps)
	set(&sw.ConfirmPolls, f.Switch.ConfirmPolls)
	set(&sw.Cooldown, f.Switch.Cooldown)
	set(&sw.RoamBelowDBm, f.Switch.RoamBelowDBm)
	set(&sw.BlacklistFor, f.Switch.BlacklistFor)
	set(&sw.LinkFailuresBeforeRoam, f.Switch.LinkFailuresBeforeRoam)
	set(&sw.OnlineTimeout, f.Switch.OnlineTimeout)
	if len(f.Connectivity.TCPTargets) > 0 {
		c.Connectivity.TCPTargets = f.Connectivity.TCPTargets
	}
	if len(f.Connectivity.DNSNames) > 0 {
		c.Connectivity.DNSNames = f.Connectivity.DNSNames
	}
	set(&c.Connectivity.Timeout, f.Connectivity.Timeout)
	set(&c.DNSHeal.Enabled, f.DNSHeal.Enabled)
	set(&c.DNSHeal.FailuresBeforeHeal, f.DNSHeal.FailuresBeforeHeal)
	set(&c.DNSHeal.HeavyCooldown, f.DNSHeal.HeavyCooldown)
	set(&c.ExitNode.Failover, f.ExitNode.Failover)
	set(&c.ExitNode.Restore, f.ExitNode.Restore)
	set(&c.ExitNode.RestoreAfterPolls, f.ExitNode.RestoreAfterPolls)

	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}
