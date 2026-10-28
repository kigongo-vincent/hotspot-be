package router

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/packages"
)

// -----------------------------------------------------------------------
// MikroTik hotspot provisioning
// -----------------------------------------------------------------------
//
// This file is the bridge between the voucher/sales domain and the
// RouterOS hotspot server. A voucher sold in the dashboard becomes a
// hotspot user on the customer's MikroTik: the customer then logs into
// the captive portal with their voucher code.
//
// Everything here talks to the router through the standardized API
// account over the WireGuard mesh (see RunRouterCommand), so no
// inbound access to the customer's router is ever required.

// HotspotUser mirrors one `/ip hotspot user` entry on the router.
type HotspotUser struct {
	Name           string `json:"name"`
	Password       string `json:"password,omitempty"`
	Profile        string `json:"profile"`
	Comment        string `json:"comment"`
	LimitUptime    string `json:"limitUptime"`
	LimitBytes     string `json:"limitBytesTotal"`
	RateLimit      string `json:"rateLimit"`
	SharedUsers    bool   `json:"sharedUsers"`
	Disabled       bool   `json:"disabled"`
	LoginFirstTime bool   `json:"loginFirstTime"`
}

// HotspotUserListResponse is the payload of GET /router/:id/hotspot/users.
type HotspotUserListResponse struct {
	Users []HotspotUser `json:"users"`
}

// ProvisionHotspotUserRequest is the payload of
// POST /router/:id/hotspot/users.
type ProvisionHotspotUserRequest struct {
	// Username is the hotspot login (typically the voucher code).
	Username string `json:"username"`
	// Password is the hotspot password. When empty the username is
	// reused as the password, which is what voucher customers expect
	// (one code, used for both fields).
	Password string `json:"password"`
	// PackageID selects the hotspot user profile to attach. When zero
	// the router's default profile is used.
	PackageID *uint `json:"packageId"`
	// Comment is stored on the router's user entry so an operator can
	// trace the entry back to the voucher it came from.
	Comment string `json:"comment"`
}

// ErrRouterNotConnected is returned when an operation needs a live
// router connection but the VPN link is not up yet.
var ErrRouterNotConnected = errors.New("router is not connected - finish the setup wizard first")

// durationPattern matches the package module's "<amount> <UNIT>" duration
// format, e.g. "24 HRS", "7 DAYS", "90 MINS".
var durationPattern = regexp.MustCompile(`^(\d+)\s*(HRS|MINS|DAYS)$`)

// routerOSDuration converts a package duration into the compact
// RouterOS format ("1h", "30m", "7d") accepted by limit-uptime.
func routerOSDuration(raw string) string {
	match := durationPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(raw)))
	if match == nil {
		return ""
	}

	amount, err := strconv.Atoi(match[1])
	if err != nil {
		return ""
	}

	switch match[2] {
	case "MINS":
		return fmt.Sprintf("%dm", amount)
	case "DAYS":
		return fmt.Sprintf("%dd", amount)
	default: // HRS
		return fmt.Sprintf("%dh", amount)
	}
}

// routerOSBytes converts a human data limit ("2GB", "500MB", "1.5GB")
// into the RouterOS byte format ("2G", "500M"). Returns "" when the
// value cannot be parsed, in which case no limit is applied.
func routerOSBytes(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}

	multiplier := ""
	switch {
	case strings.HasSuffix(trimmed, "gb"):
		multiplier = "G"
	case strings.HasSuffix(trimmed, "mb"):
		multiplier = "M"
	case strings.HasSuffix(trimmed, "kb"):
		multiplier = "K"
	case strings.HasSuffix(trimmed, "tb"):
		multiplier = "T"
	default:
		return ""
	}

	value, err := strconv.ParseFloat(strings.TrimSuffix(trimmed, multiplier), 64)
	if err != nil || value <= 0 {
		return ""
	}

	return strconv.FormatFloat(value, 'f', -1, 64) + multiplier
}

// hotspotProfileName is the router-side name of the hotspot user profile
// backing a package. RouterOS profile names cannot contain spaces, so
// the package name is slugified.
func hotspotProfileName(pkg packages.Package) string {
	name := strings.ToLower(strings.TrimSpace(pkg.Name))
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-' || r == '_':
			sb.WriteRune(r)
		case r == ' ' || r == '.' || r == '/':
			sb.WriteRune('-')
		}
	}

	slug := strings.Trim(sb.String(), "-")
	if slug == "" {
		slug = "package"
	}
	return slug
}

// EnsureHotspotProfile creates (or refreshes) the `/ip hotspot user
// profile` that backs a package on this router, together with the
// simple queue that enforces the package's speed limit.
//
// Hotspot profiles live on the router, so this is idempotent: an
// existing profile with the same name is updated in place rather than
// duplicated.
func (s *RouterService) EnsureHotspotProfile(routerID uint, pkg packages.Package) (string, error) {
	profile := hotspotProfileName(pkg)

	args := []string{
		"=name=" + profile,
		"=shared-users=1",
	}
	if uptime := routerOSDuration(pkg.Duration); uptime != "" {
		args = append(args, "=limit-uptime="+uptime)
	}
	if bytes := routerOSBytes(pkg.DataLimit); bytes != "" {
		args = append(args, "=limit-bytes-total="+bytes)
	}
	if speed := strings.TrimSpace(pkg.SpeedLimit); speed != "" {
		args = append(args, "=rate-limit="+speed)
	}

	// RouterOS addresses records by their internal id, so an existing
	// profile is located first and then updated in place. That keeps
	// the operation idempotent: re-provisioning a voucher never
	// duplicates the profile, and a changed package duration/speed
	// reaches vouchers that were already issued.
	existingID, err := s.hotspotProfileID(routerID, profile)
	if err != nil {
		return "", err
	}

	if existingID != "" {
		setArgs := append([]string{"=numbers=" + existingID}, args[1:]...)
		if _, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/profile/set", setArgs...); err != nil {
			return "", fmt.Errorf("failed to update hotspot profile %q: %w", profile, err)
		}
		return profile, nil
	}

	if _, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/profile/add", args...); err != nil {
		return "", fmt.Errorf("failed to create hotspot profile %q: %w", profile, err)
	}

	return profile, nil
}

// hotspotProfileID returns the RouterOS internal id of the named hotspot
// user profile, or "" when the profile does not exist yet.
func (s *RouterService) hotspotProfileID(routerID uint, name string) (string, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/profile/print", "=name="+name)
	if err != nil {
		return "", fmt.Errorf("failed to read hotspot profiles: %w", err)
	}
	if reply == nil || len(reply.Re) == 0 {
		return "", nil
	}
	return reply.Re[0].Map[".id"], nil
}

// ListHotspotUsers returns the hotspot users configured on the router.
func (s *RouterService) ListHotspotUsers(routerID uint) ([]HotspotUser, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/print")
	if err != nil {
		return nil, fmt.Errorf("failed to list hotspot users: %w", err)
	}

	users := make([]HotspotUser, 0)
	if reply == nil {
		return users, nil
	}

	for _, row := range reply.Re {
		users = append(users, HotspotUser{
			Name:        row.Map["name"],
			Profile:     row.Map["profile"],
			Comment:     row.Map["comment"],
			LimitUptime: row.Map["limit-uptime"],
			LimitBytes:  row.Map["limit-bytes-total"],
			RateLimit:   row.Map["rate-limit"],
			SharedUsers: row.Map["shared-users"] == "true",
			Disabled:    row.Map["disabled"] == "true",
		})
	}

	return users, nil
}

// RemoveHotspotUser deletes a hotspot user from the router. RouterOS
// addresses records by their internal id, so the entry is located first
// and removed by that id.
func (s *RouterService) RemoveHotspotUser(routerID uint, username string) error {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/print", "=name="+username)
	if err != nil {
		return fmt.Errorf("failed to find hotspot user %q: %w", username, err)
	}
	if reply == nil || len(reply.Re) == 0 {
		return fmt.Errorf("hotspot user %q not found on the router", username)
	}

	id := reply.Re[0].Map[".id"]
	if id == "" {
		return fmt.Errorf("hotspot user %q has no id on the router", username)
	}

	if _, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/remove", "=numbers="+id); err != nil {
		return fmt.Errorf("failed to remove hotspot user %q: %w", username, err)
	}

	return nil
}

// ProvisionHotspotUser creates (or updates) a hotspot user on the
// router for the given credentials and package.
func (s *RouterService) ProvisionHotspotUser(routerID uint, req ProvisionHotspotUserRequest, pkg *packages.Package) (HotspotUser, error) {
	if strings.TrimSpace(req.Username) == "" {
		return HotspotUser{}, errors.New("username is required")
	}

	password := req.Password
	if password == "" {
		password = req.Username
	}

	profile := "default"
	limitUptime := ""
	limitBytes := ""
	rateLimit := ""
	if pkg != nil {
		created, err := s.EnsureHotspotProfile(routerID, *pkg)
		if err != nil {
			return HotspotUser{}, err
		}
		profile = created
		limitUptime = routerOSDuration(pkg.Duration)
		limitBytes = routerOSBytes(pkg.DataLimit)
		rateLimit = strings.TrimSpace(pkg.SpeedLimit)
	}

	comment := req.Comment
	if comment == "" {
		comment = "hotspot-manager"
	}

	// Remove any stale entry with the same name first: RouterOS refuses
	// to add a duplicate user name, and re-provisioning a voucher must
	// be idempotent.
	_ = s.RemoveHotspotUser(routerID, req.Username)

	args := []string{
		"=name=" + req.Username,
		"=password=" + password,
		"=profile=" + profile,
		"=comment=" + comment,
	}
	if limitUptime != "" {
		args = append(args, "=limit-uptime="+limitUptime)
	}
	if limitBytes != "" {
		args = append(args, "=limit-bytes-total="+limitBytes)
	}
	if rateLimit != "" {
		args = append(args, "=rate-limit="+rateLimit)
	}

	if _, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/add", args...); err != nil {
		return HotspotUser{}, fmt.Errorf("failed to provision hotspot user %q: %w", req.Username, err)
	}

	return HotspotUser{
		Name:        req.Username,
		Profile:     profile,
		Comment:     comment,
		LimitUptime: limitUptime,
		LimitBytes:  limitBytes,
		RateLimit:   rateLimit,
		SharedUsers: true,
	}, nil
}

// ActiveHotspotUsernames returns the usernames of the clients currently
// logged into the router's hotspot. The telemetry poller uses this to
// record a voucher's first successful login.
func (s *RouterService) ActiveHotspotUsernames(routerID uint) ([]string, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/active/print")
	if err != nil {
		return nil, fmt.Errorf("failed to read active hotspot sessions: %w", err)
	}
	if reply == nil {
		return nil, nil
	}

	names := make([]string, 0, len(reply.Re))
	for _, row := range reply.Re {
		if name := strings.TrimSpace(row.Map["user"]); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// HotspotUserExists reports whether a hotspot user with the given name
// already exists on the router.
func (s *RouterService) HotspotUserExists(routerID uint, username string) (bool, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/user/print", "=name="+username)
	if err != nil {
		return false, fmt.Errorf("failed to read hotspot users: %w", err)
	}
	if reply == nil {
		return false, nil
	}
	return len(reply.Re) > 0, nil
}

// RouterUptime returns the router's uptime in seconds.
func (s *RouterService) RouterUptime(routerID uint) (time.Duration, error) {
	res, err := s.fetchSystemResource(routerID)
	if err != nil {
		return 0, err
	}
	return time.Duration(res.UptimeSeconds) * time.Second, nil
}
