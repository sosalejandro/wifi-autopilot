package sopsconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sosalejandro/wifi-autopilot/internal/core/ports"
)

var _ ports.ConfigSource = (*Source)(nil)

const plain = `
poll_interval: 45s
networks:
  - ssid: Home Network
    password: correct-horse
  - ssid: Home_5G
    weight: 1.2
switch:
  cooldown: 10m
dns_heal:
  enabled: false
`

func write(t *testing.T, content string) string {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPlainKeepsDefaults(t *testing.T) {
	s := New(write(t, plain))
	s.Decrypt = func(context.Context, string) ([]byte, error) {
		t.Fatal("plain file must not be decrypted")
		return nil, nil
	}
	c, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 45*time.Second || c.Switch.Cooldown != 10*time.Minute || c.DNSHeal.Enabled {
		t.Errorf("overrides not applied: %+v", c)
	}
	if c.Interface != "wlan0" || c.Switch.ConfirmPolls != 2 || !c.ExitNode.Failover {
		t.Errorf("defaults lost: %+v", c)
	}
	if n := c.Networks[0]; n.Security != "wpa-psk" || n.Weight != 1 || !n.Managed() {
		t.Errorf("network defaults: %+v", n)
	}
	if c.Networks[1].Managed() {
		t.Error("network without password should reuse a saved profile")
	}
	if s.Changed() {
		t.Error("unchanged file reported as changed")
	}
}

func TestLoadEncryptedUsesSops(t *testing.T) {
	s := New(write(t, "networks:\n  - ssid: x\n    password: ENC[AES256_GCM,data:abc]\nsops:\n  age: []\n"))
	s.Decrypt = func(context.Context, string) ([]byte, error) { return []byte(plain), nil }
	c, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Networks[0].Password != "correct-horse" {
		t.Errorf("decrypted content not used: %+v", c.Networks)
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":    "networks: [{ssid: a}]\npoll_intervall: 5s\n",
		"short password": "networks: [{ssid: a, password: short}]\n",
		"no networks":    "poll_interval: 30s\n",
	} {
		if _, err := New(write(t, content)).Load(context.Background()); err == nil {
			t.Errorf("%s: expected error", name)
		} else if !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("%s: error should name the file: %v", name, err)
		}
	}
}

func TestShippedExampleIsValid(t *testing.T) {
	c, err := New("../../../deploy/config.example.yaml").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Networks) == 0 || len(c.ManagedNetworks()) != len(c.Networks) {
		t.Errorf("example should provision every network: %+v", c.Networks)
	}
}
