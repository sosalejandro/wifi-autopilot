// Package tailscale implements ports.ExitNodes with the tailscale CLI.
package tailscale

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sosalejandro/wifi-autopilot/internal/core/domain"
	"github.com/sosalejandro/wifi-autopilot/internal/platform/cmdexec"
)

type Adapter struct{ Run cmdexec.Runner }

func New() *Adapter { return &Adapter{Run: cmdexec.Exec} }

type peer struct {
	ID       string
	HostName string
	DNSName  string
	Online   bool
}

func (p peer) name() string {
	if n := strings.SplitN(p.DNSName, ".", 2)[0]; n != "" {
		return n
	}
	return p.HostName
}

type status struct {
	ExitNodeStatus *struct {
		ID     string
		Online bool
	}
	Peer map[string]peer
}

func (a *Adapter) status(ctx context.Context) (*status, error) {
	out, err := a.Run(ctx, "tailscale", "status", "--json")
	if err != nil {
		return nil, err
	}
	var st status
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (a *Adapter) Current(ctx context.Context) (*domain.ExitNode, error) {
	st, err := a.status(ctx)
	if err != nil {
		return nil, err
	}
	return currentExitNode(st), nil
}

func currentExitNode(st *status) *domain.ExitNode {
	if st.ExitNodeStatus == nil {
		return nil
	}
	en := &domain.ExitNode{Online: st.ExitNodeStatus.Online}
	for _, p := range st.Peer {
		if p.ID == st.ExitNodeStatus.ID {
			en.Name, en.Online = p.name(), p.Online
		}
	}
	return en
}

func (a *Adapter) PeerOnline(ctx context.Context, name string) (bool, error) {
	st, err := a.status(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range st.Peer {
		if p.name() == name || p.HostName == name {
			return p.Online, nil
		}
	}
	return false, nil
}

func (a *Adapter) Set(ctx context.Context, name string) error {
	_, err := a.Run(ctx, "tailscale", "set", "--exit-node="+name)
	return err
}
