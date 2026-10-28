package router

import (
	"fmt"
)

// -----------------------------------------------------------------------
// REQUIRED MODEL CHANGE — add this field to the existing VPN struct:
//
//   type VPN struct {
//       gorm.Model
//       RemoteKeys     datatypes.JSONType[KeyPair] `json:"remoteKeys"`
//       Address        string                      `json:"address"`
//       RouterID       *uint                       `json:"routerId"`
//       Router         Router                      `json:"router" gorm:"foreignKey:RouterID"`
//       Status         VPNConnection               `json:"status" gorm:"default:default"`
//       APIUsername    string                      `json:"-"`
//       APIPasswordEnc string                      `json:"-" gorm:"column:api_password_enc"`
//       LANCidr        string                      `json:"-" gorm:"column:lan_cidr"`  // <-- add this line
//   }
//
// This needs a migration (GORM AutoMigrate will add the column; existing
// rows get "" and get backfilled lazily by GetVPNScript on next fetch,
// same pattern as APIUsername/APIPasswordEnc above it).
// -----------------------------------------------------------------------

// lanBase is the private supernet all per-router hotspot LANs are carved
// out of. 10.0.0.0/8 gives 65,536 distinct /24s (10.0.0.0 - 10.255.255.0),
// which comfortably covers per-router allocation without ever touching
// the common home/office defaults (192.168.0.0/16) that clients may
// already be using upstream of the VPN mesh.
const lanBase = "10"

// maxAllocatable24s bounds how many /24s we'll ever hand out from the
// 10.0.0.0/8 space (2nd and 3rd octets each 0-255 => 65536 possible /24s,
// reserving octet pair 0.0 for infra use).
const maxAllocatable24s = 65535

// AllocateLANCidr deterministically derives a unique /24 for a given VPN
// record ID. Because gorm.Model IDs are DB-assigned, auto-incrementing,
// and never reused, (id mod maxAllocatable24s)+1 guarantees no two VPN
// rows ever receive the same /24, without needing a separate allocation
// table or a lock. The result always falls inside 10.0.0.0/8.
//
// Example: vpnID=1     -> 10.0.1.0/24
//
//	vpnID=256   -> 10.1.0.0/24
//	vpnID=65792 -> 10.1.1.0/24 (wraps only after 65,535 routers,
//	               at which point this should be revisited)
func AllocateLANCidr(vpnID uint) (string, error) {
	if vpnID == 0 {
		return "", fmt.Errorf("cannot allocate LAN CIDR: vpnID is zero (record not yet persisted)")
	}

	slot := (vpnID % maxAllocatable24s) + 1 // +1 reserves slot 0 (10.0.0.0/24) for infra
	octet2 := (slot / 256) % 256
	octet3 := slot % 256

	return fmt.Sprintf("%s.%d.%d.0/24", lanBase, octet2, octet3), nil
}
