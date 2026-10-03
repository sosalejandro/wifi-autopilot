package networkmanager

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
)

const (
	profileIDPrefix   = "wifi-autopilot/"
	profileFilePrefix = "wifi-autopilot-"
)

func profileName(ssid string) string { return profileIDPrefix + ssid }
func isManaged(name string) bool     { return strings.HasPrefix(name, profileIDPrefix) }

// SyncProfiles makes ProfileDir hold exactly one keyfile per managed network: written root-only
// so passwords never appear in argv, stale managed files removed, other profiles left alone.
func (a *Adapter) SyncProfiles(ctx context.Context, networks []domain.Network) (changed int, err error) {
	want := map[string][]byte{}
	for _, n := range networks {
		want[filepath.Join(a.ProfileDir, profileFile(n.SSID))] = keyfile(a.Iface, n)
	}

	existing, err := filepath.Glob(filepath.Join(a.ProfileDir, profileFilePrefix+"*.nmconnection"))
	if err != nil {
		return 0, err
	}
	for _, path := range existing {
		if _, ok := want[path]; ok {
			continue
		}
		if err := os.Remove(path); err != nil {
			return changed, err
		}
		changed++
	}

	for path, data := range want {
		if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return changed, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return changed, err
		}
		changed++
	}

	if changed > 0 {
		if _, err := a.nmcli(ctx, "connection", "reload"); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func profileFile(ssid string) string {
	sum := sha1.Sum([]byte(ssid))
	return fmt.Sprintf("%s%s-%x.nmconnection", profileFilePrefix, unsafeFileChars.ReplaceAllString(ssid, "_"), sum[:3])
}

// profileUUID derives a stable UUID (v5 layout) from the SSID so rewrites keep the same profile.
func profileUUID(ssid string) string {
	b := sha1.Sum([]byte("wifi-autopilot:" + ssid))
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func keyfile(iface string, n domain.Network) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s\nuuid=%s\ntype=wifi\ninterface-name=%s\nautoconnect=true\n\n",
		escapeKeyfile(profileName(n.SSID)), profileUUID(n.SSID), iface)
	fmt.Fprintf(&b, "[wifi]\nmode=infrastructure\nssid=%s\n\n", escapeKeyfile(n.SSID))
	if n.Security != "open" {
		fmt.Fprintf(&b, "[wifi-security]\nkey-mgmt=%s\npsk=%s\n\n", n.Security, escapeKeyfile(n.Password))
	}
	b.WriteString("[ipv4]\nmethod=auto\n\n[ipv6]\naddr-gen-mode=default\nmethod=auto\n")
	return []byte(b.String())
}

// escapeKeyfile applies GKeyFile value escaping.
func escapeKeyfile(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	if strings.HasPrefix(s, " ") {
		s = `\s` + s[1:]
	}
	return s
}
