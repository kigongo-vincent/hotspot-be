package router

import (
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type VPNConnection string

const (
	Pending   VPNConnection = "pending"
	Connected VPNConnection = "connected"
	Default   VPNConnection = "default"
)

// Router is a managed MikroTik device. A router is reached over the
// WireGuard mesh (see wgserver.go) using its standardized API account,
// so the backend never needs inbound access to the device.
type Router struct {
	gorm.Model
	Name string `json:"name" gorm:"not null"`
	// Brand / Model describe the hardware (e.g. "MikroTik", "hAP ac²").
	Brand string `json:"brand"`
	// DeviceModel is the hardware model identifier. It is not called
	// "Model" in Go because the embedded gorm.Model already occupies
	// that field name.
	DeviceModel string `json:"model"`
	// Location is a free-text site description shown in the router list.
	Location string `json:"location"`
	// Notes is a free-text field for the operator.
	Notes string `json:"notes"`
	// IsActive is false once a router has been decommissioned - the
	// router stays in the database (and its stats history is kept) but
	// is excluded from provisioning and the active router picker.
	IsActive bool `json:"isActive" gorm:"default:true"`
	// RemoteAccess records whether the operator has opened the
	// RouterOS API/Winbox ports to the public internet for this router.
	// It is false by default - remote access must be an explicit,
	// auditable decision.
	RemoteAccess bool `json:"remoteAccess" gorm:"default:false"`
	UserID       *uint       `json:"userId"`
	User         shared.User `json:"user" gorm:"foreignKey:UserID"`
}

// CreateRouterRequest is the payload for POST /router.
type CreateRouterRequest struct {
	Name string `json:"name"`
}

// UpdateRouterRequest is the payload for PUT /router/:id. Only the
// fields present in the request are applied.
type UpdateRouterRequest struct {
	Name         *string `json:"name"`
	Brand        *string `json:"brand"`
	Model        *string `json:"model"`
	Location     *string `json:"location"`
	Notes        *string `json:"notes"`
	IsActive     *bool   `json:"isActive"`
	RemoteAccess *bool   `json:"remoteAccess"`
}

// RouterListItem is the shape the router list endpoint returns - the
// router's own fields plus the connection state and the latest
// telemetry sample, so the table can render status without a second
// round-trip per row.
type RouterListItem struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Brand        string    `json:"brand"`
	Model        string    `json:"model"`
	Location     string    `json:"location"`
	Notes        string    `json:"notes"`
	IsActive     bool      `json:"isActive"`
	RemoteAccess bool      `json:"remoteAccess"`
	CreatedAt    string    `json:"createdAt"`
	Connected    bool      `json:"connected"`
	VPNStatus    string    `json:"vpnStatus"`
	CPU          float64   `json:"cpu"`
	Memory       float64   `json:"memory"`
	ActiveUsers  int       `json:"activeUsers"`
	LastSeen     string  `json:"lastSeen"`
	Provisioned  int64   `json:"provisioned"`
	LastStatAt   *string `json:"lastStatAt"`
}

// RouterListResponse is the payload of GET /router/list.
type RouterListResponse struct {
	Routers []RouterListItem `json:"routers"`
}

type CreateVPNRequest struct {
	RouterID *uint `json:"routerId"`
}

type KeyPair struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

type VPN struct {
	gorm.Model
	RemoteKeys     datatypes.JSONType[KeyPair] `json:"remoteKeys"`
	Address        string                      `json:"address"`
	RouterID       *uint                       `json:"routerId"`
	Router         Router                      `json:"router" gorm:"foreignKey:RouterID"`
	Status         VPNConnection               `json:"status" gorm:"default:default"`
	APIUsername    string                      `json:"-"`
	APIPasswordEnc string                      `json:"-" gorm:"column:api_password_enc"`
	// LANCidr is the per-router hotspot LAN subnet (e.g. "10.0.42.0/24"),
	// allocated deterministically from the VPN row's ID the first time
	// GetVPNScript runs for a record that doesn't have one yet. Kept
	// distinct per router so routers bridged over the same VPN mesh never
	// collide on subnet.
	LANCidr string `json:"-" gorm:"column:lan_cidr"`
}

type RouterService struct {
	DB *gorm.DB
}

type RouterStat struct {
	gorm.Model
	RouterID uint    `gorm:"index;not null"`
	CPU      float64 `gorm:"not null"` // percent, 0-100
	Memory   float64 `gorm:"not null"` // percent used, 0-100
	RxBytes  uint64  `gorm:"not null"`
	TxBytes  uint64  `gorm:"not null"`

	ActiveUsers int `gorm:"not null"`
}

const RetentionWindow = 24 * time.Hour
