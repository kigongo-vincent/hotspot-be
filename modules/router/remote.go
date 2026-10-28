package router

import (
	"fmt"
)

// -----------------------------------------------------------------------
// Remote access control
// -----------------------------------------------------------------------
//
// The provisioning script deliberately blocks RouterOS management ports
// coming in from the customer's WAN interface, so the only way to reach
// a managed router is over the WireGuard mesh. Operators sometimes need
// a temporary window to reach a router directly (e.g. an on-site
// engineer using Winbox). "Remote access" is that window: it is off by
// default, must be switched on explicitly, is recorded on the router
// record, and can be closed again at any time.

// RemoteAccessComment tags the firewall rules this module manages, so
// they can be found and removed again without touching the rest of the
// managed firewall baseline.
const RemoteAccessComment = "hsmgr-remote-access"

// RemoteAccessPorts are the RouterOS management ports opened when
// remote access is enabled.
var RemoteAccessPorts = []string{"8291", "8728"}

// RemoteAccessState is the payload of GET/POST /router/:id/remote-access.
type RemoteAccessState struct {
	Enabled   bool     `json:"enabled"`
	Ports     []string `json:"ports"`
	AppliedAt string   `json:"appliedAt,omitempty"`
	Note      string   `json:"note"`
}

// SetRemoteAccessRequest is the payload of POST /router/:id/remote-access.
type SetRemoteAccessRequest struct {
	Enabled bool `json:"enabled"`
}

// firewallRule is the subset of an `/ip firewall filter` entry this
// module reasons about.
type firewallRule struct {
	ID          string
	Action      string
	Chain       string
	InInterface string
	DstPort     string
	Protocol    string
	Comment     string
}

// listFirewallRules reads the router's firewall filter chain.
func (s *RouterService) listFirewallRules(routerID uint) ([]firewallRule, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/firewall/filter/print")
	if err != nil {
		return nil, fmt.Errorf("failed to read firewall rules: %w", err)
	}
	if reply == nil {
		return nil, nil
	}

	rules := make([]firewallRule, 0, len(reply.Re))
	for _, row := range reply.Re {
		rules = append(rules, firewallRule{
			ID:          row.Map[".id"],
			Action:      row.Map["action"],
			Chain:       row.Map["chain"],
			InInterface: row.Map["in-interface"],
			DstPort:     row.Map["dst-port"],
			Protocol:    row.Map["protocol"],
			Comment:     row.Map["comment"],
		})
	}
	return rules, nil
}

// removeFirewallRule removes a single rule by its RouterOS id.
func (s *RouterService) removeFirewallRule(routerID uint, id string) error {
	if id == "" {
		return nil
	}
	if _, err := s.RunRouterCommand(routerID, "/ip/firewall/filter/remove", "=numbers="+id); err != nil {
		return fmt.Errorf("failed to remove firewall rule %s: %w", id, err)
	}
	return nil
}

// isRemoteAccessRule reports whether a rule is one of the accept rules
// this module installed to open remote access.
func isRemoteAccessRule(r firewallRule) bool {
	if r.Comment == RemoteAccessComment {
		return true
	}
	return r.Action == "accept" && r.Chain == "input" && r.InInterface == "ether1" &&
		(r.DstPort == "8291" || r.DstPort == "8728")
}

// isWanBlockRule reports whether a rule is the baseline "drop RouterOS
// management traffic from the WAN" rule created by the provisioning
// script.
func isWanBlockRule(r firewallRule) bool {
	return r.Action == "drop" && r.Chain == "input" && r.InInterface == "ether1" &&
		r.DstPort == "8291"
}

// GetRemoteAccess returns the current remote-access state for a router.
func (s *RouterService) GetRemoteAccess(routerID uint, persisted bool) (RemoteAccessState, error) {
	state := RemoteAccessState{
		Enabled: persisted,
		Ports:   RemoteAccessPorts,
		Note:    "Management ports are closed by default. Enable remote access only while you need direct access to the router.",
	}

	rules, err := s.listFirewallRules(routerID)
	if err != nil {
		// The router may be offline; fall back to the persisted flag
		// rather than failing the whole request.
		state.Enabled = persisted
		return state, nil
	}

	live := false
	for _, r := range rules {
		if isRemoteAccessRule(r) {
			live = true
			break
		}
	}
	state.Enabled = live || persisted

	return state, nil
}

// SetRemoteAccess opens or closes the router's management ports and
// persists the resulting state on the router record.
func (s *RouterService) SetRemoteAccess(routerID uint, enabled bool) (RemoteAccessState, error) {
	rules, err := s.listFirewallRules(routerID)
	if err != nil {
		return RemoteAccessState{}, err
	}

	// Always clear anything we previously installed before deciding the
	// new state - that makes the operation idempotent and removes rules
	// left behind by an earlier run.
	for _, r := range rules {
		if isRemoteAccessRule(r) {
			if err := s.removeFirewallRule(routerID, r.ID); err != nil {
				return RemoteAccessState{}, err
			}
		}
	}

	// Drop the baseline WAN block rule while remote access is on.
	if enabled {
		for _, r := range rules {
			if isWanBlockRule(r) {
				if err := s.removeFirewallRule(routerID, r.ID); err != nil {
					return RemoteAccessState{}, err
				}
			}
		}

		for _, port := range RemoteAccessPorts {
			args := []string{
				"=chain=input",
				"=action=accept",
				"=protocol=tcp",
				"=dst-port=" + port,
				"=in-interface=ether1",
				"=comment=" + RemoteAccessComment,
			}
			if _, err := s.RunRouterCommand(routerID, "/ip/firewall/filter/add", args...); err != nil {
				return RemoteAccessState{}, fmt.Errorf("failed to open port %s: %w", port, err)
			}
		}
	} else {
		// Re-install the baseline block rule so the router returns to
		// its hardened state.
		blocked := false
		for _, r := range rules {
			if isWanBlockRule(r) {
				blocked = true
				break
			}
		}
		if !blocked {
			args := []string{
				"=chain=input",
				"=action=drop",
				"=protocol=tcp",
				"=dst-port=8291",
				"=in-interface=ether1",
				"=comment=hsmgr-managed",
			}
			if _, err := s.RunRouterCommand(routerID, "/ip/firewall/filter/add", args...); err != nil {
				return RemoteAccessState{}, fmt.Errorf("failed to close the management port: %w", err)
			}
		}
	}

	state, err := s.GetRemoteAccess(routerID, enabled)
	if err != nil {
		return RemoteAccessState{}, err
	}
	return state, nil
}
