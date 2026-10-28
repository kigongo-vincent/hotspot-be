package router

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/go-routeros/routeros"
	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/auth"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---- Config ------------------------------------------------------------
var (
	envOnce               sync.Once
	BASE_URL              string
	WG_SERVER_ENDPOINT    string
	WG_SERVER_PRIVATE_KEY string
	WG_SERVER_PUBLIC_KEY  string
)

const VPN_SUBNET_BASE = "10.10.0"

// Standardized RouterOS API user provisioned on every managed router.
// This is deliberately NOT the hotspot admin user and is restricted to
// API-only access (see the user group policy in buildVPNScript). The
// username is constant and non-secret; only the per-router password is
// secret material, and it is unique per router (see generateAPICredentials).
const STANDARD_API_USERNAME = "hsmgr_api"
const STANDARD_API_GROUP = "hsmgr-api"

func loadEnv() {
	envOnce.Do(func() {
		BASE_URL = os.Getenv("BASE_URL")
		if BASE_URL == "" {
			log.Fatal("router: BASE_URL is not set - this must be an address the MikroTik can reach, not localhost")
		}

		WG_SERVER_ENDPOINT = os.Getenv("WG_SERVER_ENDPOINT")
		if WG_SERVER_ENDPOINT == "" {
			log.Fatal("router: WG_SERVER_ENDPOINT is not set")
		}
		if _, _, err := net.SplitHostPort(WG_SERVER_ENDPOINT); err != nil {
			log.Fatalf("router: WG_SERVER_ENDPOINT must be a bare host:port (no scheme), got %q: %v", WG_SERVER_ENDPOINT, err)
		}

		WG_SERVER_PRIVATE_KEY = os.Getenv("WG_SERVER_PRIVATE_KEY")
		WG_SERVER_PUBLIC_KEY = os.Getenv("WG_SERVER_PUBLIC_KEY")
		if WG_SERVER_PRIVATE_KEY == "" || WG_SERVER_PUBLIC_KEY == "" {
			log.Fatal("router: WG_SERVER_PRIVATE_KEY / WG_SERVER_PUBLIC_KEY are not set")
		}

		// Fail fast at startup if the encryption key is missing/invalid,
		// rather than discovering it the first time a router is provisioned.
		if _, err := loadCryptoKey(); err != nil {
			log.Fatalf("router: %v", err)
		}
	})
}

type CmdRequest struct {
	CMD string `json:"cmd"`
}

func (s *RouterService) cmd(c *fiber.Ctx) error {
	var body CmdRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	res, resErr := s.RunRouterCommand(4, body.CMD)
	if resErr != nil {
		return base.API_ERROR(c, resErr.Error())
	}
	return c.JSON(base.APIResponse{Data: res})
}

// func NewService(db *gorm.DB) *RouterService {
// 	loadEnv()

// 	if err := EnsureWireGuardServer(); err != nil {
// 		log.Printf("router: WARNING - WireGuard server not available: %v", err)
// 	}

// 	return &RouterService{DB: db}
// }

// In router.go, update NewService to rehydrate peers right after the
// server interface comes up - this is the actual fix that stops a
// backend/VM restart from silently orphaning already-connected routers
// (see wireguard_server.go's PERSISTENCE MODEL note for why this was
// missing and what breaks without it).

func NewService(db *gorm.DB) *RouterService {
	loadEnv()

	if err := EnsureWireGuardServer(); err != nil {
		log.Printf("router: WARNING - WireGuard server not available: %v", err)
	} else if err := RehydratePeersFromDB(db); err != nil {
		log.Printf("router: WARNING - failed to rehydrate vpn peers: %v", err)
	}

	// Starts the dashboard's telemetry sampling loop (see
	// telemetry_poller.go). Safe to call every time NewService runs -
	// guarded internally by sync.Once, so only the first call in this
	// process actually launches the goroutine.
	StartTelemetryPoller(db)

	return &RouterService{DB: db}
}

// ---- Keys + addressing --------------------------------------------------

func GenerateInternalKeys() datatypes.JSONType[KeyPair] {
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		log.Fatal(err)
	}

	return datatypes.NewJSONType(KeyPair{
		Public:  privateKey.PublicKey().String(),
		Private: privateKey.String(),
	})
}

// generateAPIPassword creates a fresh, cryptographically random password for
// the standardized per-router RouterOS API user. The username itself is the
// constant STANDARD_API_USERNAME; only the password varies per router, and
// it is regenerated (never reused) on every call - including rotation.
func generateAPIPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate api password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *RouterService) allocateVPNAddress() (string, error) {
	var used []string
	if err := s.DB.Model(&VPN{}).Pluck("address", &used).Error; err != nil {
		return "", err
	}

	taken := make(map[string]bool, len(used))
	for _, addr := range used {
		taken[addr] = true
	}

	for i := 2; i < 255; i++ {
		candidate := fmt.Sprintf("%s.%d/32", VPN_SUBNET_BASE, i)
		if !taken[candidate] {
			return candidate, nil
		}
	}

	return "", errors.New("no free VPN addresses left in pool")
}

// vpnHost strips the CIDR suffix (e.g. "/32") from a stored VPN address so
// it can be used as a bare host for dialing, e.g. "10.10.0.5/32" -> "10.10.0.5".
func vpnHost(address string) string {
	if idx := strings.IndexByte(address, '/'); idx != -1 {
		return address[:idx]
	}
	return address
}

// ---- RouterOS script ------------------------------------------------

func fetchModeForURL(url string) string {
	if strings.HasPrefix(url, "https://") {
		return "https"
	}
	return "http"
}

// -----------------------------------------------------------------------
// Config resolved once per call, no hardcoded network values anywhere.
// -----------------------------------------------------------------------

// LANConfig describes the per-router LAN the hotspot bridge will serve.
// Callers MUST supply a unique CIDR per router/site when provisioning
// multiple routers that may end up bridged over the same VPN mesh —
// reusing 192.168.88.0/24 (or any single subnet) across sites is the
// most common cause of routing collisions in multi-tenant hotspot
// deployments.
type LANConfig struct {
	CIDR       string   // e.g. "10.24.3.0/24" — assigned per router at provisioning time
	DNSServers []string // e.g. []string{"8.8.8.8", "1.1.1.1"} — now a param, not hardcoded
}

// resolved holds the concrete values derived from CIDR math, computed
// once so the template never has to guess at gateway/pool boundaries.
type resolvedLAN struct {
	network    string // e.g. 10.24.3.0
	prefixLen  int    // e.g. 24
	gateway    string // e.g. 10.24.3.1  (first usable host)
	poolStart  string // e.g. 10.24.3.10 (skip a small reserved range for static assignments)
	poolEnd    string // e.g. 10.24.3.254
	cidrSuffix string // "/24"
	dnsCSV     string // "8.8.8.8,1.1.1.1"
}

// reservedForStatic is how many addresses after the gateway we skip
// before starting the DHCP pool, leaving room for static reservations
// (AP uplinks, printers, etc.) — a standard hotspot-network convention.
const reservedForStatic = 9

func resolveLAN(cfg LANConfig) (*resolvedLAN, error) {
	ip, ipNet, err := net.ParseCIDR(cfg.CIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid LAN CIDR %q: %w", cfg.CIDR, err)
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("LAN CIDR %q is not IPv4", cfg.CIDR)
	}

	ones, bits := ipNet.Mask.Size()
	if bits-ones < 4 {
		// Need room for network, gateway, a small reserved block, and at
		// least a handful of DHCP leases. /30, /31, /32 etc. are rejected.
		return nil, fmt.Errorf("LAN CIDR %q is too small for a hotspot subnet (need at least /28 or larger)", cfg.CIDR)
	}

	networkIP := ipNet.IP.To4()
	gatewayIP := incIP(networkIP, 1)
	poolStartIP := incIP(networkIP, 1+reservedForStatic)

	// Last usable host = broadcast - 1
	broadcast := lastIP(ipNet)
	poolEndIP := incIP(broadcast, -1)

	if !ipLess(poolStartIP, poolEndIP) {
		return nil, fmt.Errorf("LAN CIDR %q leaves no room for a DHCP pool after reserving %d static addresses", cfg.CIDR, reservedForStatic)
	}

	dns := cfg.DNSServers
	if len(dns) == 0 {
		return nil, fmt.Errorf("at least one DNS server must be configured (set via env, no hardcoded fallback)")
	}

	return &resolvedLAN{
		network:    networkIP.String(),
		prefixLen:  ones,
		gateway:    gatewayIP.String(),
		poolStart:  poolStartIP.String(),
		poolEnd:    poolEndIP.String(),
		cidrSuffix: "/" + strconv.Itoa(ones),
		dnsCSV:     strings.Join(dns, ","),
	}, nil
}

func incIP(ip net.IP, delta int) net.IP {
	v4 := ip.To4()
	out := make(net.IP, 4)
	copy(out, v4)
	val := uint32(out[0])<<24 | uint32(out[1])<<16 | uint32(out[2])<<8 | uint32(out[3])
	val = uint32(int64(val) + int64(delta))
	out[0] = byte(val >> 24)
	out[1] = byte(val >> 16)
	out[2] = byte(val >> 8)
	out[3] = byte(val)
	return out
}

func lastIP(ipNet *net.IPNet) net.IP {
	ip := ipNet.IP.To4()
	mask := ipNet.Mask
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func ipLess(a, b net.IP) bool {
	a4, b4 := a.To4(), b.To4()
	for i := 0; i < 4; i++ {
		if a4[i] != b4[i] {
			return a4[i] < b4[i]
		}
	}
	return false
}

// -----------------------------------------------------------------------
// Walled-garden whitelist — pre-login access only, sourced from env.
// -----------------------------------------------------------------------

// WalledGardenDomainsFromEnv reads a comma-separated domain list from the
// given env var (e.g. HOTSPOT_WALLED_GARDEN_DOMAINS="example.com,*.stripe.com")
// and returns a cleaned slice. Empty/whitespace entries are dropped.
// This governs ONLY pre-login walled-garden access, per spec — it does
// NOT install any persistent post-login filtering.
func WalledGardenDomainsFromEnv(envVar string) []string {
	raw := os.Getenv(envVar)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		d := strings.ToLower(strings.TrimSpace(p))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// -----------------------------------------------------------------------
// Main builder
// -----------------------------------------------------------------------

// VPNScriptParams bundles everything buildVPNScript needs. Nothing here
// has a hardcoded network default; LAN, DNS, and walled-garden values are
// all supplied explicitly by the caller (typically read from env/config
// at the call site, per your existing app conventions).
type VPNScriptParams struct {
	ServerPublicKey string
	VPNAddress      string
	RouterName      string
	AuthToken       string
	APIUsername     string
	APIPassword     string
	LAN             LANConfig
	WalledGarden    []string // pre-login allowed domains, e.g. from WalledGardenDomainsFromEnv(...)
}

func buildVPNScript(p VPNScriptParams) (string, error) {
	lan, err := resolveLAN(p.LAN)
	if err != nil {
		return "", fmt.Errorf("buildVPNScript: %w", err)
	}

	callbackURL := fmt.Sprintf("%s/router/vpn/complete", BASE_URL)
	callbackMode := fetchModeForURL(callbackURL)

	epHost, epPort, err := net.SplitHostPort(WG_SERVER_ENDPOINT)
	if err != nil {
		epHost = WG_SERVER_ENDPOINT
		epPort = "13231"
	}

	// Sanitize routerName to create a valid RouterOS Hotspot DNS domain name
	cleanName := strings.ToLower(strings.TrimSpace(p.RouterName))
	cleanName = strings.ReplaceAll(cleanName, " ", "-")
	var sb strings.Builder
	for _, r := range cleanName {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	hotspotDNS := sb.String()
	if hotspotDNS == "" {
		hotspotDNS = "hotspot"
	}
	if !strings.Contains(hotspotDNS, ".") {
		hotspotDNS += ".lan"
	}

	// Also used as the per-router hotspot folder name so multiple routers
	// provisioned against the same backend never collide on disk paths.
	hotspotFolder := "hotspot-" + strings.TrimSuffix(hotspotDNS, ".lan")
	if hotspotFolder == "hotspot-" {
		hotspotFolder = "hotspot-" + cleanName
	}

	// Build a RouterOS array literal for the walled-garden domain list.
	wgDomainsLiteral := buildRouterOSArrayLiteral(p.WalledGarden)

	lanNetworkCIDR := lan.network + lan.cidrSuffix

	script := fmt.Sprintf(`{
:local serverPubKey "%s"
:local epAddr "%s"
:local epPort %s
:local vpnAddress "%s"
:local hotspotDns "%s"
:local hotspotFolder "%s"
:local callbackUrl "%s"
:local authToken "%s"
:local apiUser "%s"
:local apiPass "%s"
:local apiGroup "%s"

:local lanGateway "%s"
:local lanCidrSuffix "%s"
:local lanNetwork "%s"
:local poolStart "%s"
:local poolEnd "%s"
:local dnsServers "%s"

:local walledGardenDomains %s

# 1. RouterOS Version Check
:put ">>> [hsmgr 1/10] Checking RouterOS version"
:local osVer [/system resource get version]
:local majorVer [:tonum [:pick $osVer 0 [:find $osVer "."]]]

:if ($majorVer < 7) do={
    :log error "Hotspot Manager requires RouterOS v7+ for WireGuard support."
    :put "------------------------------------------------------------"
    :put "ERROR: RouterOS v7+ is required for WireGuard VPN connections."
    :put ("Your router is currently running RouterOS v" . $osVer . ".")
    :put "Please upgrade your RouterOS in System -> Packages."
    :put "------------------------------------------------------------"
    :error "RouterOS v7+ required."
} else={
    # 2. Clean up existing Hotspot, DHCP, and Bridge setup
    # NOTE: we no longer match on a hardcoded subnet - we remove by the
    # bridge/interface name instead, so cleanup works regardless of
    # whatever LAN CIDR a previous provisioning run used.
    :put ">>> [hsmgr 2/10] Removing previous hotspot, DHCP and bridge configuration"
    /ip hotspot remove [find name="hotspot1"]
    /ip hotspot profile remove [find name="hotspot-profile"]

    /ip dhcp-server remove [find name="hotspot-dhcp"]
    /ip dhcp-server network remove [find address=$lanNetwork]
    /ip dhcp-server network remove [find comment="hsmgr-managed"]
    /ip pool remove [find name="hotspot-pool"]

    /ip address remove [find interface="hotspot-bridge"]

    /interface bridge port remove [find bridge="hotspot-bridge"]
    /interface bridge remove [find name="hotspot-bridge"]

    # 3. Clean up existing WireGuard setup
    :put ">>> [hsmgr 3/10] Removing previous WireGuard configuration"
    /interface wireguard peers remove [find interface="wg-vpn"]
    /ip address remove [find interface="wg-vpn"]
    /interface wireguard remove [find name="wg-vpn"]

    # 4. Setup WireGuard
    :put ">>> [hsmgr 4/10] Creating WireGuard interface and peer"
    /interface wireguard add name=wg-vpn listen-port=$epPort
    :delay 1s

    :local wgPublicKey [/interface wireguard get [find name="wg-vpn"] public-key]
    :put ("Extracted WireGuard Public Key: " . $wgPublicKey)

    /interface wireguard peers add interface=wg-vpn public-key=$serverPubKey endpoint-address=$epAddr endpoint-port=$epPort allowed-address=0.0.0.0/0 persistent-keepalive=25s
    /ip address add address=$vpnAddress interface=wg-vpn

    # 4b. Standardized remote-access API user (API-only, no winbox/ssh/ftp/web,
    # no password/user-management/reboot rights). This is the ONLY account
    # the backend uses to run commands against this router.
    :put ">>> [hsmgr 5/10] Creating standardized API-only management user"
    /user remove [find name=$apiUser]
    /user group remove [find name=$apiGroup]
    /user group add name=$apiGroup policy=api,read,write,!local,!telnet,!ssh,!ftp,!reboot,!password,!sensitive,!web,!winbox,!sniff,!romon,!policy
    /user add name=$apiUser password=$apiPass group=$apiGroup

    # 5. Report Public Key + API user back to Server with Bearer Auth
    :put ">>> [hsmgr 6/10] Reporting public key back to the server"
    :local jsonPayload "{\"publicKey\":\"$wgPublicKey\",\"apiUsername\":\"$apiUser\"}"
    :local headers ("Content-Type: application/json,Authorization: Bearer " . $authToken)
    :do {
      /tool fetch url=$callbackUrl http-method=post http-header-field=$headers http-data=$jsonPayload mode=%s output=none
    } on-error={
      :log warning "Callback to API server failed with HTTP error. Continuing script..."
      :put "WARNING: Callback POST to server failed. Verify API server reachability and token validity."
    }

    # 6. Re-create Bridge and assign LAN ports safely
    :put ">>> [hsmgr 7/10] Creating hotspot bridge and assigning LAN ports"
    /interface bridge add name=hotspot-bridge

    :foreach i in=[/interface ethernet find] do={
      :local ifName [/interface ethernet get $i name]
      :if ($ifName != "ether1") do={
        /interface bridge port remove [find interface=$ifName]
        /interface bridge port add bridge=hotspot-bridge interface=$ifName
      }
    }

    /ip address add address=($lanGateway . $lanCidrSuffix) interface=hotspot-bridge
    /ip pool add name=hotspot-pool ranges=($poolStart . "-" . $poolEnd)
    /ip dhcp-server add name=hotspot-dhcp interface=hotspot-bridge address-pool=hotspot-pool disabled=no;
    /ip dhcp-server network add address=$lanNetwork gateway=$lanGateway dns-server=$dnsServers comment="hsmgr-managed"

    # 6b. Clone the router's default hotspot folder as the base template
    # for this router's landing page, instead of writing hotspot1's files
    # in place. This keeps the shipped default untouched and gives every
    # router its own folder that can be safely customized/overwritten.
    :put ">>> [hsmgr 8/10] Cloning the default hotspot landing-page template"
    :if ([:len [/file find name="hotspot"]] > 0) do={
      /file copy [find name="hotspot"] name=$hotspotFolder
      :put ("Cloned default hotspot template to /" . $hotspotFolder)
    } else={
      :log warning "Default /hotspot template not found on this device; creating an empty folder instead."
      /file add name=$hotspotFolder type=directory
    }

    # 7. Re-create Hotspot Profile pointing at the cloned folder
    :put ">>> [hsmgr 9/10] Creating hotspot profile and hotspot server"
    /ip hotspot profile add name=hotspot-profile hotspot-address=$lanGateway dns-name=$hotspotDns html-directory=$hotspotFolder login-by=http-chap,cookie
    /ip hotspot add name=hotspot1 interface=hotspot-bridge address-pool=hotspot-pool profile=hotspot-profile disabled=no

    # 7b. Walled garden: pre-login access only. Authenticated (post-login)
    # traffic is governed entirely by the firewall forward rules below -
    # this section does NOT add any persistent post-login domain filtering.
    :put ">>> [hsmgr 10/10] Applying walled garden and firewall baseline"
    /ip hotspot walled-garden remove [find comment="hsmgr-managed"]
    :foreach domain in=$walledGardenDomains do={
      /ip hotspot walled-garden add dst-host=$domain action=allow comment="hsmgr-managed"
    }

    # 8. Refresh Firewall baseline
    /ip firewall filter remove [find comment="hsmgr-managed"]
    /ip firewall nat remove [find comment="hsmgr-managed"]

    /ip firewall filter add chain=input action=accept connection-state=established,related comment="hsmgr-managed"
    /ip firewall filter add chain=input action=accept in-interface=wg-vpn comment="hsmgr-managed"
    /ip firewall filter add chain=input action=drop in-interface=ether1 protocol=tcp dst-port=8291 comment="hsmgr-managed"
    /ip firewall filter add chain=forward action=accept connection-state=established,related comment="hsmgr-managed"
    /ip firewall filter add chain=forward action=accept in-interface=hotspot-bridge out-interface=wg-vpn comment="hsmgr-managed"
    /ip firewall filter add chain=forward action=drop connection-state=invalid comment="hsmgr-managed"
    /ip firewall nat add chain=srcnat out-interface=wg-vpn action=masquerade comment="hsmgr-managed"

    :put ">>> [hsmgr] Setup complete"
}
}`,
		p.ServerPublicKey, epHost, epPort, p.VPNAddress, hotspotDNS, hotspotFolder,
		callbackURL, p.AuthToken, p.APIUsername, p.APIPassword, STANDARD_API_GROUP,
		lan.gateway, lan.cidrSuffix, lanNetworkCIDR, lan.poolStart, lan.poolEnd, lan.dnsCSV,
		wgDomainsLiteral,
		callbackMode)

	return strings.ReplaceAll(script, "\u00a0", " "), nil
}

// buildRouterOSArrayLiteral renders a Go string slice as a RouterOS
// array literal, e.g. {"example.com"; "*.stripe.com"}. Empty input
// renders as an empty array {} — the walled-garden loop then simply
// runs zero iterations rather than needing a nil check in the script.
func buildRouterOSArrayLiteral(domains []string) string {
	if len(domains) == 0 {
		return "{}"
	}
	quoted := make([]string, len(domains))
	for i, d := range domains {
		// Strip scheme (http://, https://) and port — RouterOS dst-host
		// accepts only a hostname or IP, never a full URL.
		host := d
		if idx := strings.Index(host, "://"); idx != -1 {
			host = host[idx+3:]
		}
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		quoted[i] = `"` + strings.ReplaceAll(host, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(quoted, "; ") + "}"
}

// buildRotateAPIUserScript renders a minimal RouterOS script that only
// resets the standardized API user's password, without touching WireGuard,
// the hotspot, DHCP, or firewall. Used for credential rotation so a full
// re-provision isn't required just to cycle a password.
func buildRotateAPIUserScript(apiUsername, newPassword string) string {
	script := fmt.Sprintf(`{
:local apiUser "%s"
:local newPass "%s"
:local apiGroup "%s"

/user remove [find name=$apiUser]
/user group remove [find name=$apiGroup]
/user group add name=$apiGroup policy=api,read,write,!local,!telnet,!ssh,!ftp,!reboot,!password,!sensitive,!web,!winbox,!sniff,!romon,!policy
/user add name=$apiUser password=$newPass group=$apiGroup
}`, apiUsername, newPassword, STANDARD_API_GROUP)

	return strings.ReplaceAll(script, "\u00a0", " ")
}

func GenerateBootstrapCommand(routerID uint) (string, error) {
	loadEnv()

	tkn, err := auth.GenerateToken(routerID, shared.Default)
	if err != nil {
		return "", err
	}

	scriptURL := fmt.Sprintf("%s/router/vpn/script/%d", BASE_URL, routerID)
	mode := fetchModeForURL(scriptURL)

	// The generated script prints "[hsmgr N/10]" progress markers before
	// every major stage. If import fails, the router prints the error line
	// AFTER the last marker, which tells you exactly which stage failed -
	// not just an opaque "line 107 column 26". We deliberately do NOT wrap
	// /import in :do/on-error because that swallows the parser's error
	// detail on RouterOS versions older than 7.16.
	return fmt.Sprintf(
		`/tool fetch url="%v" http-header-field="Authorization: Bearer %v" dst-path="vpn.rsc" mode=%v; :delay 2s; :put ">>> [hsmgr] Configuration downloaded, applying (the last [hsmgr N/10] marker identifies the failing stage)..."; /import file-name="vpn.rsc"; :delay 1s; /file remove "vpn.rsc"`,
		scriptURL, tkn, mode,
	), nil
}

// ---- Service methods --------------------------------------------------

func (s *RouterService) CreateVPN(body CreateVPNRequest) (string, error) {
	var tmpRouter Router
	if err := s.DB.First(&tmpRouter, body.RouterID).Error; err != nil {
		return "", err
	}

	address, addrErr := s.allocateVPNAddress()
	if addrErr != nil {
		return "", addrErr
	}

	apiPassword, credErr := generateAPIPassword()
	if credErr != nil {
		return "", credErr
	}

	encPassword, encErr := EncryptSecret(apiPassword)
	if encErr != nil {
		return "", fmt.Errorf("failed to encrypt api credentials: %w", encErr)
	}
	apiPassword = "" // discard plaintext as soon as it's encrypted

	tmp := VPN{
		RouterID: body.RouterID,
		Address:  address,
		Status:   Pending,
		// Standardized RouterOS API credentials for this router. The
		// password is stored only as AES-256-GCM ciphertext (APIPasswordEnc);
		// plaintext exists only transiently above and inside buildVPNScript.
		APIUsername:    STANDARD_API_USERNAME,
		APIPasswordEnc: encPassword,
	}
	if err := s.DB.Create(&tmp).Error; err != nil {
		return "", err
	}

	return GenerateBootstrapCommand(*body.RouterID)
}

func (s *RouterService) CreateRouter(c *fiber.Ctx) error {
	var body CreateRouterRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	UserID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "failed to get user"+uErr.Error())
	}

	var cmd string
	txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		tmp := Router{Name: body.Name, UserID: &UserID}
		if err := tx.Create(&tmp).Error; err != nil {
			return err
		}

		txService := &RouterService{DB: tx}
		generatedCmd, cmdErr := txService.CreateVPN(CreateVPNRequest{RouterID: &tmp.ID})
		if cmdErr != nil {
			return cmdErr
		}

		cmd = generatedCmd
		return nil
	})

	if txErr != nil {
		return base.API_ERROR(c, "failed to create the router: "+txErr.Error())
	}

	return c.JSON(base.APIResponse{Data: fiber.Map{"cmd": cmd}})
}

func (s *RouterService) GetVPNScript(c *fiber.Ctx) error {
	routerID, idErr := base.GetUserIDFromCtx(c)
	if idErr != nil {
		return base.API_ERROR(c, "unauthorized")
	}

	var vpn VPN
	if err := s.DB.Where("router_id = ?", routerID).First(&vpn).Error; err != nil {
		return base.API_ERROR(c, "no vpn found for this router")
	}

	var rtr Router
	if err := s.DB.First(&rtr, routerID).Error; err != nil {
		return base.API_ERROR(c, "router not found")
	}

	tkn, err := auth.GenerateToken(routerID, shared.Default)
	if err != nil {
		return base.API_ERROR(c, "failed to generate token")
	}

	// Resolve the standardized API credentials. If this VPN record predates
	// the standardized-user change (no credentials provisioned yet),
	// generate and persist a set now so older routers get upgraded the
	// next time their script is fetched, instead of failing outright.
	apiUsername := vpn.APIUsername
	var apiPassword string
	if apiUsername == "" || vpn.APIPasswordEnc == "" {
		apiUsername = STANDARD_API_USERNAME

		var genErr error
		apiPassword, genErr = generateAPIPassword()
		if genErr != nil {
			return base.API_ERROR(c, "failed to generate api credentials")
		}

		encPassword, encErr := EncryptSecret(apiPassword)
		if encErr != nil {
			return base.API_ERROR(c, "failed to encrypt api credentials")
		}

		vpn.APIUsername = apiUsername
		vpn.APIPasswordEnc = encPassword
		if err := s.DB.Save(&vpn).Error; err != nil {
			return base.API_ERROR(c, "failed to persist api credentials")
		}
	} else {
		var decErr error
		apiPassword, decErr = DecryptSecret(vpn.APIPasswordEnc)
		if decErr != nil {
			return base.API_ERROR(c, "failed to decrypt api credentials")
		}
	}

	// Resolve the per-router LAN CIDR. Older VPN records predating this
	// change won't have one yet — allocate and persist it now, same
	// upgrade-in-place pattern used above for API credentials, so
	// existing routers get a non-colliding subnet the next time their
	// script is fetched instead of the request failing.
	if vpn.LANCidr == "" {
		cidr, allocErr := AllocateLANCidr(vpn.ID)
		if allocErr != nil {
			return base.API_ERROR(c, "failed to allocate lan subnet")
		}
		vpn.LANCidr = cidr
		if err := s.DB.Save(&vpn).Error; err != nil {
			return base.API_ERROR(c, "failed to persist lan subnet")
		}
	}

	dnsServers := dnsServersFromEnv("HOTSPOT_DNS_SERVERS")
	if len(dnsServers) == 0 {
		return base.API_ERROR(c, "no dns servers configured")
	}

	script, serr := buildVPNScript(VPNScriptParams{
		ServerPublicKey: WG_SERVER_PUBLIC_KEY,
		VPNAddress:      vpn.Address,
		RouterName:      rtr.Name,
		AuthToken:       tkn,
		APIUsername:     apiUsername,
		APIPassword:     apiPassword,
		LAN: LANConfig{
			CIDR:       vpn.LANCidr,
			DNSServers: dnsServers,
		},
		WalledGarden: WalledGardenDomainsFromEnv("HOTSPOT_WALLED_GARDEN_DOMAINS"),
	})
	if serr != nil {
		return base.API_ERROR(c, "failed to generate")
	}
	apiPassword = "" // discard plaintext reference now that it's embedded in script

	c.Set("Content-Type", "text/plain")
	return c.SendString(script)
}

// dnsServersFromEnv reads a comma-separated DNS server list from the given
// env var. No hardcoded fallback — if it's unset, callers must treat that
// as a configuration error rather than silently defaulting to a
// third-party resolver (e.g. 8.8.8.8) that wasn't explicitly chosen.
func dnsServersFromEnv(envVar string) []string {
	raw := os.Getenv(envVar)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

type CompleteVPNRequest struct {
	PublicKey string `json:"publicKey"`
	// ApiUsername is reported back by the router script for confirmation /
	// logging purposes. It is expected to equal STANDARD_API_USERNAME and is
	// optional so this remains backward compatible with routers running an
	// older bootstrap script that doesn't send it.
	ApiUsername string `json:"apiUsername"`
}

func (s *RouterService) CompleteVPN(c *fiber.Ctx) error {
	routerID, idErr := base.GetUserIDFromCtx(c)
	if idErr != nil {
		return base.API_ERROR(c, "unauthorized")
	}

	var body CompleteVPNRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	if body.PublicKey == "" {
		return base.API_ERROR(c, "missing public key")
	}

	var vpn VPN
	if err := s.DB.Where("router_id = ?", routerID).First(&vpn).Error; err != nil {
		return base.API_ERROR(c, "no vpn found for this router")
	}

	if err := AddOrUpdateServerPeer(body.PublicKey, vpn.Address); err != nil {
		return base.API_ERROR(c, "failed to register vpn peer: "+err.Error())
	}

	// RemoteKeys holds only the router's WireGuard public key - it is
	// entirely separate from APIUsername/APIPasswordEnc, so recording it
	// here can never clobber or interact with the API credentials.
	vpn.RemoteKeys = datatypes.NewJSONType(KeyPair{Public: body.PublicKey})
	vpn.Status = Connected
	if err := s.DB.Save(&vpn).Error; err != nil {
		return base.API_ERROR(c, "failed to save connection")
	}

	return c.JSON(base.APIResponse{Message: "vpn connected"})
}

func (s *RouterService) CheckStatus(c *fiber.Ctx) error {
	UserID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "failed to get the user")
	}

	var rtr Router
	if err := s.DB.Where("user_id = ?", UserID).First(&rtr).Error; err != nil {
		return c.JSON(base.APIResponse{Data: fiber.Map{
			"hasRouter": false,
			"connected": false,
		}})
	}

	var vpn VPN
	if err := s.DB.Where("router_id = ?", rtr.ID).First(&vpn).Error; err != nil {
		cmd, cmdErr := s.CreateVPN(CreateVPNRequest{RouterID: &rtr.ID})
		if cmdErr != nil {
			return base.API_ERROR(c, "failed to set up vpn for this router: "+cmdErr.Error())
		}

		return c.JSON(base.APIResponse{Data: fiber.Map{
			"hasRouter": true,
			"connected": false,
			"cmd":       cmd,
		}})
	}

	if vpn.Status == Connected {
		return c.JSON(base.APIResponse{Data: fiber.Map{
			"hasRouter": true,
			"connected": true,
		}})
	}

	cmd, cmdErr := GenerateBootstrapCommand(rtr.ID)
	if cmdErr != nil {
		return base.API_ERROR(c, "failed to regenerate connection command")
	}

	return c.JSON(base.APIResponse{Data: fiber.Map{
		"hasRouter": true,
		"connected": false,
		"cmd":       cmd,
	}})
}

// DashboardStatPoint is a single point on the CPU/memory/traffic/users
// time-series charts. Field names/JSON tags match the frontend's
// DashboardStatPoint interface exactly (camelCase over the wire).
type DashboardStatPoint struct {
	Time        string  `json:"time"`
	CPU         float64 `json:"cpu"`
	Memory      float64 `json:"memory"`
	Download    float64 `json:"download"` // bytes/sec since the previous sample
	Upload      float64 `json:"upload"`   // bytes/sec since the previous sample
	ActiveUsers int     `json:"activeUsers"`
}

// DashboardRouterSummary is the minimal router identity the dashboard
// card needs - matches the frontend's DashboardRouterSummary interface.
type DashboardRouterSummary struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// RouterDashboard is the full response shape for GET /router/dashboard.
// Matches the frontend's RouterDashboard interface field-for-field.
type RouterDashboard struct {
	HasRouter bool                    `json:"hasRouter"`
	Connected bool                    `json:"connected"`
	Cmd       string                  `json:"cmd,omitempty"`
	Router    *DashboardRouterSummary `json:"router"`
	Stats     []DashboardStatPoint    `json:"stats"`
}

// DashboardHistoryLimit caps how many recent RouterStat rows are turned
// into chart points per request. At PollInterval=30s, 60 points is the
// last 30 minutes - adjust alongside PollInterval if you want a longer
// or shorter visible window.
const DashboardHistoryLimit = 60

func zeroStatPoint() DashboardStatPoint {
	return DashboardStatPoint{Time: "now"}
}

// GetDashboard is the single endpoint the Routers UI page calls.
//
// Route: GET /router/dashboard (same auth middleware as CheckStatus/GetVPNScript)
func (s *RouterService) GetDashboard(c *fiber.Ctx) error {
	UserID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "failed to get the user")
	}

	var rtr Router
	if err := s.DB.Where("user_id = ?", UserID).First(&rtr).Error; err != nil {
		return c.JSON(base.APIResponse{Data: RouterDashboard{
			HasRouter: false,
			Connected: false,
			Router:    nil,
			Stats:     []DashboardStatPoint{zeroStatPoint()},
		}})
	}

	var vpn VPN
	connected := false
	var cmd string

	if err := s.DB.Where("router_id = ?", rtr.ID).First(&vpn).Error; err == nil {
		connected = vpn.Status == Connected

		if !connected {
			if generatedCmd, cmdErr := GenerateBootstrapCommand(rtr.ID); cmdErr == nil {
				cmd = generatedCmd
			} else {
				log.Println("GetDashboard: failed to generate bootstrap command:", cmdErr)
			}
		}
	} else {
		if generatedCmd, cmdErr := s.CreateVPN(CreateVPNRequest{RouterID: &rtr.ID}); cmdErr == nil {
			cmd = generatedCmd
		} else {
			log.Println("GetDashboard: failed to create vpn:", cmdErr)
		}
	}

	stats := []DashboardStatPoint{zeroStatPoint()}
	if connected {
		if series, err := s.loadStatSeries(rtr.ID); err != nil {
			log.Println("GetDashboard: loadStatSeries error:", err)
		} else if len(series) > 0 {
			stats = series
		}
	}

	resp := RouterDashboard{
		HasRouter: true,
		Connected: connected,
		Cmd:       cmd,
		Router: &DashboardRouterSummary{
			ID:   rtr.ID,
			Name: rtr.Name,
		},
		Stats: stats,
	}

	return c.JSON(base.APIResponse{Data: resp})
}

// loadStatSeries reads the most recent persisted RouterStat rows for a
// router and converts them into DashboardStatPoints, computing
// download/upload as a byte-delta-over-time rate between each
// consecutive pair of samples (RouterOS only reports cumulative
// counters, never an instantaneous rate - see telemetry_poller.go).
// The oldest point in the returned series has Download/Upload=0 since
// there is no earlier sample to diff against.
func (s *RouterService) loadStatSeries(routerID uint) ([]DashboardStatPoint, error) {
	var rows []RouterStat
	err := s.DB.
		Where("router_id = ?", routerID).
		Order("created_at desc").
		Limit(DashboardHistoryLimit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	// Rows came back newest-first; reverse to chronological order so the
	// chart's X axis reads left-to-right as oldest-to-newest, and so the
	// delta calc below compares each row against the one before it.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	points := make([]DashboardStatPoint, len(rows))
	for i, row := range rows {
		p := DashboardStatPoint{
			Time:        row.CreatedAt.In(kampala).Format("15:04:05"),
			CPU:         row.CPU,
			Memory:      row.Memory,
			ActiveUsers: row.ActiveUsers,
		}

		if i > 0 {
			prev := rows[i-1]
			elapsed := row.CreatedAt.Sub(prev.CreatedAt).Seconds()
			if elapsed > 0 {
				// Counters can reset (router reboot) - a negative delta
				// means the counter wrapped/reset, not that traffic was
				// negative, so treat that case as 0 rather than a
				// nonsensical negative chart value.
				if row.RxBytes >= prev.RxBytes {
					p.Download = float64(row.RxBytes-prev.RxBytes) / elapsed
				}
				if row.TxBytes >= prev.TxBytes {
					p.Upload = float64(row.TxBytes-prev.TxBytes) / elapsed
				}
			}
		}

		points[i] = p
	}

	return points, nil
}

// RunCommand is unchanged from the original signature so existing call
// sites keep compiling as-is: it dials a router directly given an IP and
// explicit credentials.
func RunCommand(routerVPNIP, username, password, command string, args ...string) (*routeros.Reply, error) {
	// 1. Connect to the RouterOS API port over the VPN IP
	address := fmt.Sprintf("%s:8728", vpnHost(routerVPNIP))
	client, err := routeros.Dial(address, username, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to router API: %w", err)
	}
	defer client.Close()

	// 2. Build and run the command sentence
	sentence := append([]string{command}, args...)
	reply, err := client.RunArgs(sentence)
	if err != nil {
		return nil, fmt.Errorf("command execution failed: %w", err)
	}

	return reply, nil
}

// RunRouterCommand is the standardized entry point for talking to a managed
// router: callers only need a routerID, not an IP or credentials. It looks
// up the router's VPN address and its standardized API credentials
// (APIUsername/APIPasswordEnc), decrypts the password only for the
// duration of this call, and delegates to RunCommand. This is additive -
// existing callers of RunCommand are untouched; new call sites should
// prefer this method. Errors never include the decrypted password.
func (s *RouterService) RunRouterCommand(routerID uint, command string, args ...string) (*routeros.Reply, error) {
	var vpn VPN
	if err := s.DB.Where("router_id = ?", routerID).First(&vpn).Error; err != nil {
		return nil, fmt.Errorf("no vpn found for router %d: %w", routerID, err)
	}

	if vpn.Status != Connected {
		return nil, fmt.Errorf("router %d vpn is not connected (status=%v)", routerID, vpn.Status)
	}

	if vpn.APIUsername == "" || vpn.APIPasswordEnc == "" {
		return nil, fmt.Errorf("router %d has no standardized api credentials provisioned yet", routerID)
	}

	apiPassword, err := DecryptSecret(vpn.APIPasswordEnc)
	if err != nil {
		return nil, fmt.Errorf("router %d: failed to decrypt api credentials: %w", routerID, err)
	}

	reply, cmdErr := RunCommand(vpn.Address, vpn.APIUsername, apiPassword, command, args...)
	apiPassword = "" // best-effort clear of the local reference
	return reply, cmdErr
}

// rotateCredential generates a fresh password for the router's
// standardized API user and returns the username, the plaintext (for
// immediate application to the device) and the encrypted form (for
// storage). The plaintext never leaves this function except to the
// caller that applies it.
func (s *RouterService) rotateCredential(routerID uint) (username, plaintext, encPassword string, err error) {
	var vpn VPN
	if err := s.DB.Where("router_id = ?", routerID).First(&vpn).Error; err != nil {
		return "", "", "", fmt.Errorf("no vpn found for router %d: %w", routerID, err)
	}

	username = vpn.APIUsername
	if username == "" {
		username = STANDARD_API_USERNAME
	}

	newPassword, genErr := generateAPIPassword()
	if genErr != nil {
		return "", "", "", genErr
	}

	encPassword, encErr := EncryptSecret(newPassword)
	if encErr != nil {
		return "", "", "", fmt.Errorf("failed to encrypt rotated credentials: %w", encErr)
	}

	return username, newPassword, encPassword, nil
}

// RotateRouterCredentials generates a brand new random password for the
// router's standardized API user, persists it (encrypted) only after the
// router itself confirms the change, and returns the one-time RouterOS
// script the caller must push to the router to apply it. Use this instead
// of full re-provisioning when you just need to cycle a password - e.g. on
// a routine rotation schedule or after a suspected leak.
//
// This is a two-step flow by design: the DB is not updated until the
// router has actually accepted the new password, so a failed/interrupted
// rotation never leaves the stored credential out of sync with the device.
func (s *RouterService) RotateRouterCredentials(routerID uint) (script string, newEncPassword string, err error) {
	username, newPassword, encPassword, err := s.rotateCredential(routerID)
	if err != nil {
		return "", "", err
	}

	script = buildRotateAPIUserScript(username, newPassword)
	newPassword = "" // discard plaintext once embedded in the script

	return script, encPassword, nil
}

// ConfirmCredentialRotation persists a previously-generated encrypted
// password after the caller has verified the router accepted it (e.g. by
// successfully dialing with the new credentials). Pass the newEncPassword
// value returned by RotateRouterCredentials.
func (s *RouterService) ConfirmCredentialRotation(routerID uint, newEncPassword string) error {
	var vpn VPN
	if err := s.DB.Where("router_id = ?", routerID).First(&vpn).Error; err != nil {
		return fmt.Errorf("no vpn found for router %d: %w", routerID, err)
	}

	if vpn.APIUsername == "" {
		vpn.APIUsername = STANDARD_API_USERNAME
	}
	vpn.APIPasswordEnc = newEncPassword

	if err := s.DB.Save(&vpn).Error; err != nil {
		return fmt.Errorf("failed to persist rotated credentials for router %d: %w", routerID, err)
	}
	return nil
}
