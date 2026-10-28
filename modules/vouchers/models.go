package vouchers

import (
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/gorm"
)

type VoucherService struct {
	DB *gorm.DB
}

// VoucherStatus mirrors the frontend's three-state lifecycle.
type VoucherStatus string

const (
	VoucherProvisioned VoucherStatus = "PROVISIONED"
	VoucherUsed        VoucherStatus = "USED"
	VoucherExpired     VoucherStatus = "EXPIRED"
)

// VoucherFormat mirrors the "Voucher Format" dropdown in the UI exactly:
// Alphanumeric lowercase, Alphanumeric uppercase, Numbers only, Uppercase
// letters, Lowercase letters (in that order).
type VoucherFormat string

const (
	FormatAlphanumericLower VoucherFormat = "ALPHANUMERIC_LOWER"
	FormatAlphanumericUpper VoucherFormat = "ALPHANUMERIC_UPPER"
	FormatNumeric           VoucherFormat = "NUMERIC"
	FormatUppercase         VoucherFormat = "UPPERCASE"
	FormatLowercase         VoucherFormat = "LOWERCASE"
)

// charsetFor returns the character set to sample from for a given format.
func (f VoucherFormat) charset() string {
	switch f {
	case FormatAlphanumericUpper:
		return "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	case FormatNumeric:
		return "0123456789"
	case FormatUppercase:
		return "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	case FormatLowercase:
		return "abcdefghijklmnopqrstuvwxyz"
	case FormatAlphanumericLower:
		fallthrough
	default:
		return "0123456789abcdefghijklmnopqrstuvwxyz"
	}
}

// IsValid reports whether f is one of the five formats the UI offers.
func (f VoucherFormat) IsValid() bool {
	switch f {
	case FormatAlphanumericLower, FormatAlphanumericUpper, FormatNumeric, FormatUppercase, FormatLowercase:
		return true
	default:
		return false
	}
}

// Voucher is a generated access code tied to exactly one Package (the plan
// it grants), owned by the User who generated it, and optionally attached
// to an Agent (a company member who sold/distributed it).
//
// A voucher moves through the lifecycle PROVISIONED -> USED. The sale
// module (which shares this table) owns the transition and records who
// paid, through which channel, and when - see the sale-attribution
// fields below.
type Voucher struct {
	gorm.Model
	Username   string            `json:"username"`
	Status     VoucherStatus     `json:"status" gorm:"default:PROVISIONED"`
	FirstLogin string            `json:"firstLogin"`
	ExpiresOn  string            `json:"expiresOn"`
	UseCase    string            `json:"useCase"`
	Note       string            `json:"note"`
	Format     VoucherFormat     `json:"format" gorm:"default:ALPHANUMERIC_LOWER"`
	CodeLength int               `json:"codeLength" gorm:"default:8"`
	PackageID  *uint             `json:"packageId"`
	Package    *packages.Package `json:"package" gorm:"foreignKey:PackageID"`
	UserID     *uint             `json:"userId"`
	User       *shared.User      `json:"user" gorm:"foreignKey:UserID"`
	AgentID    *uint             `json:"agentId"`
	Agent      *shared.User      `json:"agent" gorm:"foreignKey:AgentID"`

	// ---- sale attribution -------------------------------------------
	// These four fields are written by the sales module when a voucher
	// is sold. They live on the voucher row itself (rather than in a
	// parallel sales table) because "selling" a voucher is a state
	// transition of an existing, pre-provisioned code.

	// Channel records where the sale came from (e.g. "mobile-money",
	// "agent", "manual"). Blank for free allocations.
	Channel string `json:"channel" gorm:"index"`
	// Payer is the payer's phone number (blank for free allocations).
	Payer string `json:"payer" gorm:"index"`
	// PayerName is the payer's display name.
	PayerName string `json:"payerName"`
	// SoldAt is the timestamp the voucher was sold.
	SoldAt *time.Time `json:"soldAt" gorm:"index"`

	// ---- provisioning state -----------------------------------------
	// RouterID is the MikroTik this voucher was provisioned onto.
	RouterID *uint `json:"routerId" gorm:"index"`
	// Provisioned is true once the voucher's hotspot user exists on
	// the router.
	Provisioned bool `json:"provisioned" gorm:"default:false"`
	// ProvisionedAt is when the hotspot user was created on the router.
	ProvisionedAt *time.Time `json:"provisionedAt"`
	// ExpiresAt mirrors ExpiresOn as a real timestamp so expiry can be
	// queried in SQL. ExpiresOn stays as the human-readable string the
	// UI renders.
	ExpiresAt *time.Time `json:"expiresAt" gorm:"index"`
	// FirstLoginAt mirrors FirstLogin as a real timestamp, recorded the
	// first time the voucher's hotspot user appears in a router's
	// active session table. It is what makes "paid but never
	// connected" measurable.
	FirstLoginAt *time.Time `json:"firstLoginAt" gorm:"index"`
}

// VoucherRequest is the request body for creating or updating a voucher.
// This matches what the form actually collects: PackageID (required —
// every voucher is attached to exactly one of the caller's own packages),
// UseCase, Note, Format, CodeLength, Quantity (create only — how many
// vouchers to generate in one call), and an optional AgentID.
// Username/status/firstLogin/expiresOn are server-generated on create and
// are not client-editable.
type VoucherRequest struct {
	PackageID  uint          `json:"packageId"`
	UseCase    string        `json:"useCase"`
	Note       string        `json:"note"`
	Format     VoucherFormat `json:"format"`
	CodeLength int           `json:"codeLength"`
	Quantity   int           `json:"quantity"`
	AgentID    *uint         `json:"agentId"`
}

// VoucherResponse is returned for single-voucher endpoints. PackageName is
// denormalized here so the frontend table can render "package" directly,
// matching the shape the existing Voucher table expects, without a second
// lookup on the client.
type VoucherResponse struct {
	ID              uint          `json:"ID"`
	CreatedAt       string        `json:"CreatedAt"`
	Username        string        `json:"username"`
	PackageID       uint          `json:"packageId"`
	PackageName     string        `json:"package"`
	Status          string        `json:"status"`
	FirstLogin      string        `json:"firstLogin"`
	ExpiresOn       string        `json:"expiresOn"`
	UseCase         string        `json:"useCase"`
	Note            string        `json:"note"`
	Format          VoucherFormat `json:"format"`
	CodeLength      int           `json:"codeLength"`
	PackagePrice    string        `json:"packagePrice"`
	PackageDuration string        `json:"packageDuration"`
	AgentID         *uint         `json:"agentId"`
	AgentName       string        `json:"agentName,omitempty"`
	Channel         string        `json:"channel"`
	Payer           string        `json:"payer"`
	PayerName       string        `json:"payerName"`
	SoldAt          string        `json:"soldAt,omitempty"`
	Provisioned     bool          `json:"provisioned"`
	RouterID        *uint         `json:"routerId,omitempty"`
}

// VoucherListResponse is returned by the list endpoint.
type VoucherListResponse struct {
	Vouchers []VoucherResponse `json:"vouchers"`
}

// DeleteManyRequest is the body for bulk-deleting vouchers — backs the
// toolbar's "delete selected" bulk action.
type DeleteManyRequest struct {
	IDs []uint `json:"ids"`
}
