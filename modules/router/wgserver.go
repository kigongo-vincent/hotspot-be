package router

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

// ---- Server-side WireGuard interface --------------------------------
//
// wgctrl (already a dependency) can CONFIGURE an existing WireGuard
// interface - set its private key, listen port, and peers - but it
// explicitly does not create the interface itself. That's a separate,
// OS-specific step:
//   - Linux: native kernel module, via `ip link add <name> type wireguard`
//   - macOS: no native kernel WireGuard, so we shell out to `wireguard-go`
//     (userspace implementation) instead. macOS's utun driver only
//     accepts interface names matching `utun[0-9]*` - it will refuse any
//     other name outright - so on darwin we use a fixed utunN name
//     instead of the prod interface name. This only affects local
//     development; production (Linux) is unaffected and keeps using
//     the real "wg-server0" name.
//
// Unlike the per-router keys in VPN.InternalKeys (one keypair per VPN
// row - a design mismatch fixed here), the SERVER has exactly one stable
// keypair, shared as the peer public key across every router's config.
// It comes from WG_SERVER_PRIVATE_KEY / WG_SERVER_PUBLIC_KEY in the
// environment - generate once with `wg genkey` / `wg pubkey`, and never
// rotate without also updating every already-connected router's peer
// config, since they'd otherwise be trusting a key that no longer exists.
//
// PERSISTENCE MODEL (important - read before debugging "lost connection"):
//   Router peers (public key + allowed-ips) live ONLY in the running
//   WireGuard process's memory once pushed there via wgctrl - there is
//   no config file or kernel-level persistence backing them on macOS,
//   and even on Linux a peer added only via wgctrl (not written into
//   wg0.conf) does not survive `wg-quick down/up`, only a plain process
//   restart of a still-running kernel module. The ONLY durable record of
//   "this router should be a peer" is the `vpns` table (Status=Connected,
//   RemoteKeys.Public, Address). Previously, nothing ever read that table
//   back out after a restart - peers were added exactly once, reactively,
//   from CompleteVPN's callback, and never again. Restarting the backend
//   (or, on macOS, restarting wireguard-go/the VM) silently orphaned
//   every already-connected router: the interface came back up with zero
//   peers, CHR's own config was untouched and still tried to dial in, but
//   the server had no memory of it - handshakes could still occur (CHR
//   has the right server key) but traffic was dropped since there was no
//   peer to route decrypted packets to/from.
//   RehydratePeersFromDB (below) fixes this: call it once at startup,
//   after EnsureWireGuardServer, to re-push every Connected router's peer
//   config from the DB into the live WireGuard interface.
//
// macOS ROUTE PERSISTENCE (also fixed here):
//   On Linux, `ip address add dev <iface> <cidr>` installs the subnet
//   route as a side effect of assigning the address - no separate step
//   needed. On macOS, the kernel's automatically-inferred route for a
//   point-to-point utun interface (visible as a "USc" flag in `netstat
//   -rn`) has been observed to silently disappear during otherwise-
//   healthy operation (peer handshakes and traffic counters kept
//   incrementing the whole time - only the route vanished). addRoute()
//   explicitly (re-)installs the subnet route on macOS every time the
//   server interface is brought up, so we don't depend on the kernel's
//   inference.

const (
	// WGServerInterfaceNameLinux is the real, production interface name.
	WGServerInterfaceNameLinux = "wg-server0"

	// WGServerInterfaceNameDarwin is a local-dev-only stand-in, required
	// because macOS's utun driver rejects any name outside utun[0-9]*.
	// Picked high enough to be unlikely to collide with other utun users
	// (VPN clients, Docker Desktop, etc.) on a dev machine.
	WGServerInterfaceNameDarwin = "utun9"

	wgServerAddress = "10.10.0.1/24" // .1 reserved for the server, matches VPN_SUBNET_BASE
)

// WGServerInterfaceName is the interface name actually used at runtime,
// resolved once based on OS. Production (Linux) behavior is unchanged.
var WGServerInterfaceName = defaultServerInterfaceName()

func defaultServerInterfaceName() string {
	if runtime.GOOS == "darwin" {
		return WGServerInterfaceNameDarwin
	}
	return WGServerInterfaceNameLinux
}

var (
	wgServerOnce sync.Once
	wgServerErr  error
)

// EnsureWireGuardServer brings up the server-side WireGuard interface if
// it isn't already running, and configures it (private key, listen port)
// via wgctrl. Safe to call repeatedly - only does real work once per
// process. Call this once at app startup, before any router can connect.
func EnsureWireGuardServer() error {
	wgServerOnce.Do(func() {
		wgServerErr = bringUpServerInterface()
	})
	return wgServerErr
}

func bringUpServerInterface() error {
	loadEnv()

	privateKeyStr := WG_SERVER_PRIVATE_KEY
	if privateKeyStr == "" {
		return fmt.Errorf("router: WG_SERVER_PRIVATE_KEY is not set - generate one with `wg genkey` and set WG_SERVER_PRIVATE_KEY / WG_SERVER_PUBLIC_KEY (see `wg pubkey`)")
	}

	privateKey, err := wgtypes.ParseKey(privateKeyStr)
	if err != nil {
		return fmt.Errorf("router: invalid WG_SERVER_PRIVATE_KEY: %w", err)
	}

	if !interfaceExists(WGServerInterfaceName) {
		if err := createInterface(WGServerInterfaceName); err != nil {
			return fmt.Errorf("router: failed to create %s: %w", WGServerInterfaceName, err)
		}
		if err := assignAddress(WGServerInterfaceName, wgServerAddress); err != nil {
			return fmt.Errorf("router: failed to assign address to %s: %w", WGServerInterfaceName, err)
		}
		if err := bringInterfaceUp(WGServerInterfaceName); err != nil {
			return fmt.Errorf("router: failed to bring %s up: %w", WGServerInterfaceName, err)
		}
	}

	// Always (re-)assert the subnet route, even if the interface already
	// existed from a previous call in this process. This is what fixes
	// the "tunnel stays healthy, ping quietly stops working" symptom on
	// macOS - see the ROUTE PERSISTENCE note above. No-op on Linux.
	if err := addRoute(WGServerInterfaceName, wgServerAddress); err != nil {
		return fmt.Errorf("router: failed to add route for %s: %w", WGServerInterfaceName, err)
	}

	listenPort, err := parseWGListenPort(WG_SERVER_ENDPOINT)
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}

	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("router: failed to open wgctrl client: %w", err)
	}
	defer client.Close()

	err = client.ConfigureDevice(WGServerInterfaceName, wgtypes.Config{
		PrivateKey:   &privateKey,
		ListenPort:   &listenPort,
		ReplacePeers: false, // never wipe existing router peers on restart
	})
	if err != nil {
		return fmt.Errorf("router: failed to configure %s via wgctrl: %w", WGServerInterfaceName, err)
	}

	return nil
}

// AddOrUpdateServerPeer registers (or updates) a router's peer entry on the
// server-side WireGuard interface, so the tunnel actually passes traffic in
// both directions once the router reports its public key. Call this from
// CompleteVPN, after the router's public key is known and its VPN address
// has been allocated.
func AddOrUpdateServerPeer(routerPublicKey string, vpnAddress string) error {
	if err := EnsureWireGuardServer(); err != nil {
		return err
	}

	pubKey, err := wgtypes.ParseKey(routerPublicKey)
	if err != nil {
		return fmt.Errorf("router: invalid router public key: %w", err)
	}

	_, ipNet, err := net.ParseCIDR(vpnAddress)
	if err != nil {
		return fmt.Errorf("router: invalid vpn address %q: %w", vpnAddress, err)
	}

	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("router: failed to open wgctrl client: %w", err)
	}
	defer client.Close()

	err = client.ConfigureDevice(WGServerInterfaceName, wgtypes.Config{
		ReplacePeers: false,
		Peers: []wgtypes.PeerConfig{
			{
				PublicKey:         pubKey,
				ReplaceAllowedIPs: true,
				AllowedIPs:        []net.IPNet{*ipNet},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("router: failed to add/update peer: %w", err)
	}

	return nil
}

// RemoveServerPeer deletes a router's peer entry from the server-side
// WireGuard interface. Call this when a VPN row is deleted or a router's
// keypair is rotated, so stale peers don't silently accumulate on the
// interface.
func RemoveServerPeer(routerPublicKey string) error {
	if err := EnsureWireGuardServer(); err != nil {
		return err
	}

	pubKey, err := wgtypes.ParseKey(routerPublicKey)
	if err != nil {
		return fmt.Errorf("router: invalid router public key: %w", err)
	}

	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("router: failed to open wgctrl client: %w", err)
	}
	defer client.Close()

	err = client.ConfigureDevice(WGServerInterfaceName, wgtypes.Config{
		ReplacePeers: false,
		Peers: []wgtypes.PeerConfig{
			{
				PublicKey: pubKey,
				Remove:    true,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("router: failed to remove peer: %w", err)
	}

	return nil
}

// RehydratePeersFromDB re-registers every Connected router's WireGuard
// peer from its DB record. Call this once at startup, right after
// EnsureWireGuardServer succeeds (see NewService) - it is the fix for the
// "connection lost after restart" issue: peer state lives only in the
// running WireGuard process's memory (see PERSISTENCE MODEL note above),
// so every restart needs this to restore what the DB already knows
// should be connected. Best-effort per row: one router's bad/missing key
// logs a warning and is skipped rather than aborting startup for every
// other router.
func RehydratePeersFromDB(db *gorm.DB) error {
	if err := EnsureWireGuardServer(); err != nil {
		return fmt.Errorf("router: cannot rehydrate peers, server interface not up: %w", err)
	}

	var vpns []VPN
	if err := db.Where("status = ?", Connected).Find(&vpns).Error; err != nil {
		return fmt.Errorf("router: failed to load connected vpns for rehydration: %w", err)
	}

	restored := 0
	for _, vpn := range vpns {
		pubKey := vpn.RemoteKeys.Data().Public
		if pubKey == "" {
			log.Printf("router: rehydrate: vpn for router %d has no remote public key recorded, skipping", vpn.RouterID)
			continue
		}

		if err := AddOrUpdateServerPeer(pubKey, vpn.Address); err != nil {
			log.Printf("router: rehydrate: failed to restore peer for router %d: %v", vpn.RouterID, err)
			continue
		}
		restored++
	}

	log.Printf("router: rehydrated %d/%d connected vpn peer(s) onto %s", restored, len(vpns), WGServerInterfaceName)
	return nil
}

// parseWGListenPort extracts the numeric port from a "host:port" endpoint
// string, since that's what the WireGuard interface itself needs to bind
// to locally (the host part is only relevant to the far end dialing in).
func parseWGListenPort(endpoint string) (int, error) {
	_, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return 0, fmt.Errorf("WG_SERVER_ENDPOINT %q is not a valid host:port: %w", endpoint, err)
	}

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0, fmt.Errorf("WG_SERVER_ENDPOINT port %q is not numeric: %w", portStr, err)
	}

	return port, nil
}

// ---- OS-specific interface creation ----------------------------------
// wgctrl explicitly leaves interface creation out of scope - these are
// the two paths that actually bring the interface into existence before
// wgctrl can configure it.

func interfaceExists(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

func createInterface(name string) error {
	switch runtime.GOOS {
	case "linux":
		// Native kernel WireGuard module.
		return runCmd("ip", "link", "add", name, "type", "wireguard")
	case "darwin":
		// macOS has no native kernel WireGuard - use the userspace
		// implementation instead. Requires `wireguard-go` installed
		// (e.g. `brew install wireguard-go`) and on PATH.
		//
		// macOS's utun driver only accepts names matching utun[0-9]*,
		// so `name` here must already be a valid utunN (see
		// WGServerInterfaceNameDarwin) - wireguard-go will fail fast
		// with a clear error otherwise.
		if _, err := exec.LookPath("wireguard-go"); err != nil {
			return fmt.Errorf("wireguard-go not found on PATH - install it (e.g. `brew install wireguard-go`) for local WireGuard support on macOS")
		}
		return runCmd("wireguard-go", name)
	default:
		return fmt.Errorf("unsupported OS %q for creating a WireGuard interface - create %s manually and this will pick it up", runtime.GOOS, name)
	}
}

func assignAddress(name, cidr string) error {
	switch runtime.GOOS {
	case "linux":
		return runCmd("ip", "address", "add", "dev", name, cidr)
	case "darwin":
		// macOS's ifconfig does not accept CIDR notation (e.g.
		// "10.10.0.1/24") - it wants a separate address and netmask, in
		// the form: ifconfig <name> inet <addr> <dest-addr> netmask <mask>
		// wireguard-go's tun is a point-to-point-style interface with no
		// distinct peer address at this layer, so the same address is
		// used for both the local and "destination" slots.
		ip, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		mask := net.IP(ipNet.Mask).String()
		return runCmd("ifconfig", name, "inet", ip.String(), ip.String(), "netmask", mask)
	default:
		return fmt.Errorf("unsupported OS %q for assigning an address", runtime.GOOS)
	}
}

// addRoute explicitly (re-)installs the subnet route for the VPN CIDR on
// the given interface. No-op on Linux, where `ip address add` already
// installs the equivalent route as a side effect of address assignment.
// Required on macOS - see the ROUTE PERSISTENCE note above this file's
// constants. Safe to call repeatedly: an already-present route makes
// `route add` exit non-zero with "File exists" on stderr, which is
// treated as success here rather than a real failure.
func addRoute(name, cidr string) error {
	switch runtime.GOOS {
	case "linux":
		return nil
	case "darwin":
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		err = runCmd("route", "add", "-net", ipNet.String(), "-interface", name)
		if err != nil && routeAlreadyExists(err) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unsupported OS %q for adding a route", runtime.GOOS)
	}
}

// routeAlreadyExists reports whether a `route add` failure was just
// macOS/BSD's "File exists" complaint about a route that's already
// present - not a real error worth failing startup over.
func routeAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "File exists")
}

func bringInterfaceUp(name string) error {
	switch runtime.GOOS {
	case "linux":
		return runCmd("ip", "link", "set", "up", "dev", name)
	case "darwin":
		return runCmd("ifconfig", name, "up")
	default:
		return fmt.Errorf("unsupported OS %q for bringing an interface up", runtime.GOOS)
	}
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w (output: %s)", name, args, err, string(out))
	}
	return nil
}
