#!/usr/bin/env bash
#
# setup-wg-server.sh
#
# Bootstraps the persistent WireGuard SERVER interface that the Go
# `router` package expects to exist and be reachable at WG_SERVER_ENDPOINT.
# Each MikroTik router dials THIS interface as a peer (see
# buildVPNScript(): peers add ... endpoint-address=$epAddr).
#
# Works on:
#   - macOS   (dev/testing - e.g. via UTM, Homebrew's wireguard-tools)
#   - Linux   (prod - e.g. EC2, apt/yum + wg-quick + systemd)
#
# IMPORTANT macOS CAVEAT (learned the hard way today):
#   macOS has no native wg-quick/systemd service story. This script will
#   create the interface with wireguard-go + wg, but it will NOT persist
#   across reboots and will NOT survive without the wireguard-go process
#   running. On macOS this is for LOCAL DEV/TESTING ONLY. Also: if this
#   host is behind any NAT/VM bridge (UTM Shared Network, home router,
#   CGNAT, etc.), WG_SERVER_ENDPOINT must be an address actually reachable
#   from wherever your test MikroTik lives - not just "the IP ifconfig
#   shows on this interface." This is exactly the class of bug that ate
#   your afternoon.
#
# On Linux/EC2 this uses wg-quick + systemd and IS persistent/production-
# grade, given a real reachable IP or a security-group-opened UDP port.
#
# Usage:
#   sudo ./setup-wg-server.sh <listen_port> <server_vpn_ip/cidr>
# Example:
#   sudo ./setup-wg-server.sh 13231 10.10.0.1/24

set -euo pipefail

WG_IFACE="wg0"
LISTEN_PORT="${1:-13231}"
SERVER_VPN_ADDR="${2:-10.10.0.1/24}"

OS="$(uname -s)"

# ---------------------------------------------------------------------
# Shared: key generation (identical on both platforms)
# ---------------------------------------------------------------------
ensure_keys() {
  local conf_dir="$1"
  mkdir -p "$conf_dir"
  chmod 700 "$conf_dir"

  if [[ -f "$conf_dir/server_private.key" ]]; then
    echo "==> Reusing existing server keypair"
  else
    echo "==> Generating new server keypair"
    umask 077
    wg genkey | tee "$conf_dir/server_private.key" | wg pubkey > "$conf_dir/server_public.key"
  fi

  SERVER_PRIVATE_KEY="$(cat "$conf_dir/server_private.key")"
  SERVER_PUBLIC_KEY="$(cat "$conf_dir/server_public.key")"
}

print_summary() {
  echo ""
  echo "============================================================"
  echo " WireGuard server is up on $OS."
  echo ""
  echo " Set these in your Go backend's environment:"
  echo ""
  echo "   WG_SERVER_ENDPOINT=<reachable-ip-or-host>:${LISTEN_PORT}"
  echo "   WG_SERVER_PRIVATE_KEY=${SERVER_PRIVATE_KEY}"
  echo "   WG_SERVER_PUBLIC_KEY=${SERVER_PUBLIC_KEY}"
  echo ""
  echo " WG_SERVER_ENDPOINT must be reachable from the MikroTik's WAN -"
  echo " it becomes \$epAddr in buildVPNScript(). If this host sits"
  echo " behind NAT (home router, UTM Shared Network, CGNAT), that IP"
  echo " is NOT what ifconfig/ip shows on this interface - it needs"
  echo " port-forwarding or a public/EIP address in front of it."
  echo "============================================================"
}

# ---------------------------------------------------------------------
# Linux (production / EC2)
# ---------------------------------------------------------------------
setup_linux() {
  if [[ $EUID -ne 0 ]]; then
    echo "Run this as root (sudo) on Linux." >&2
    exit 1
  fi

  local conf_dir="/etc/wireguard"
  local conf="${conf_dir}/${WG_IFACE}.conf"

  echo "==> [Linux] Installing WireGuard tools if missing"
  if ! command -v wg &>/dev/null; then
    if command -v apt-get &>/dev/null; then
      apt-get update && apt-get install -y wireguard
    elif command -v yum &>/dev/null; then
      yum install -y wireguard-tools
    elif command -v dnf &>/dev/null; then
      dnf install -y wireguard-tools
    else
      echo "Unsupported package manager - install wireguard-tools manually." >&2
      exit 1
    fi
  fi

  ensure_keys "$conf_dir"

  echo "==> [Linux] Writing $conf"
  cat > "$conf" <<EOF
[Interface]
Address = ${SERVER_VPN_ADDR}
ListenPort = ${LISTEN_PORT}
PrivateKey = ${SERVER_PRIVATE_KEY}
# Peers below are managed dynamically by the Go backend via wgctrl
# (AddOrUpdateServerPeer). Do not hand-edit in production - manual
# [Peer] blocks here will drift from the VPN table in the DB.

# Uncomment only if routers need to reach beyond this host (e.g. the
# public internet) through the tunnel, not just the backend process:
# PostUp = iptables -A FORWARD -i %i -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
# PostDown = iptables -D FORWARD -i %i -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE
EOF
  chmod 600 "$conf"

  echo "==> [Linux] Enabling IP forwarding"
  sysctl -w net.ipv4.ip_forward=1
  grep -qxF 'net.ipv4.ip_forward=1' /etc/sysctl.conf || echo 'net.ipv4.ip_forward=1' >> /etc/sysctl.conf

  echo "==> [Linux] Opening firewall for UDP ${LISTEN_PORT} (local firewall only - also open this in your EC2 Security Group!)"
  if command -v ufw &>/dev/null; then
    ufw allow "${LISTEN_PORT}/udp"
  elif command -v firewall-cmd &>/dev/null; then
    firewall-cmd --permanent --add-port="${LISTEN_PORT}/udp"
    firewall-cmd --reload
  else
    echo "No local firewall manager detected - fine on stock EC2 AMIs (SG is the real gate)." >&2
  fi

  echo "==> [Linux] Bringing up ${WG_IFACE} via systemd (persistent across reboots)"
  systemctl enable "wg-quick@${WG_IFACE}"
  systemctl restart "wg-quick@${WG_IFACE}"

  print_summary
  echo ""
  echo "REMINDER (EC2-specific): open UDP ${LISTEN_PORT} in the instance's"
  echo "Security Group inbound rules, and use the instance's Elastic IP"
  echo "(not the private IP) as WG_SERVER_ENDPOINT."
}

# ---------------------------------------------------------------------
# macOS (local dev/testing only - not persistent)
# ---------------------------------------------------------------------
setup_macos() {
  local conf_dir="$HOME/.wireguard-dev"

  echo "==> [macOS] Checking for wireguard-tools (wg) and wireguard-go"
  if ! command -v wg &>/dev/null || ! command -v wireguard-go &>/dev/null; then
    if ! command -v brew &>/dev/null; then
      echo "Homebrew not found - install from https://brew.sh first." >&2
      exit 1
    fi
    echo "==> [macOS] Installing via Homebrew"
    brew install wireguard-tools wireguard-go
  fi

  ensure_keys "$conf_dir"

  # macOS's wireguard-go REQUIRES the name to literally match utun[0-9]*
  # (or the bare word "utun", which tells it to auto-pick the next free
  # number). It cannot accept an arbitrary name like "wg0" - that's the
  # exact error you just hit. So on macOS we always ask for "utun" and
  # let it choose, then resolve whatever it actually created.
  echo "==> [macOS] Creating a utun interface via wireguard-go (auto-numbered)"
  if [[ -z "$(sudo wg show interfaces 2>/dev/null)" ]]; then
    sudo wireguard-go utun
    sleep 1
  else
    echo "==> [macOS] A WireGuard interface already exists, reusing it"
  fi

  # Find whichever utunN wireguard-go actually created
  local real_iface
  real_iface="$(sudo wg show interfaces | tr ' ' '\n' | tail -n1)"
  if [[ -z "$real_iface" ]]; then
    echo "Could not determine the utun interface created by wireguard-go." >&2
    echo "Check for errors above - it may have failed to start." >&2
    exit 1
  fi
  echo "==> [macOS] wireguard-go created: ${real_iface}"

  echo "==> [macOS] Configuring $real_iface (mapped from ${WG_IFACE})"
  sudo ifconfig "$real_iface" inet "${SERVER_VPN_ADDR%%/*}" "${SERVER_VPN_ADDR%%/*}" netmask 255.255.255.0
  sudo ifconfig "$real_iface" up

  sudo wg set "$real_iface" private-key "$conf_dir/server_private.key" listen-port "$LISTEN_PORT"

  # No wg-quick equivalent, so no automatic route from AllowedIPs - add
  # the subnet route explicitly (this is the exact step that silently
  # bit us earlier with the client-side peer).
  sudo route add -net "${SERVER_VPN_ADDR%.*}.0/24" -interface "$real_iface" 2>/dev/null || true

  echo "==> [macOS] Enabling IP forwarding (session-only, resets on reboot)"
  sudo sysctl -w net.inet.ip.forwarding=1

  print_summary
  echo ""
  echo "macOS NOTES:"
  echo "  - Interface is ${real_iface} - wireguard-go on macOS can ONLY"
  echo "    create names matching utun[0-9]*, never an arbitrary name like"
  echo "    '${WG_IFACE}'. Point wgctrl/Go code at ${real_iface} if it"
  echo "    needs an explicit interface name for local testing."
  echo "  - This does NOT survive reboot. Re-run this script after restart,"
  echo "    or better: just test client<->server pairing here, and treat"
  echo "    EC2/Linux as the only persistent target."
  echo "  - If testing against a MikroTik running in UTM, double- and"
  echo "    triple-check WG_SERVER_ENDPOINT is reachable from the CHR VM's"
  echo "    actual network path (UTM Shared Network NATs the VM - see"
  echo "    today's debugging session for the full failure mode)."
}

case "$OS" in
  Linux)
    setup_linux
    ;;
  Darwin)
    setup_macos
    ;;
  *)
    echo "Unsupported OS: $OS" >&2
    exit 1
    ;;
esac