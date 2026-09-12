package packages

import (
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Currency string

const (
	UGX Currency = "UGX"
)

type Price struct {
	Value    int      `json:"value"`
	Currency Currency `json:"currency"`
}

// Package is a hotspot package (name, price, duration, data/speed caps).
// It lives in shared/ rather than modules/packages/models.go because it's
// a persisted domain entity other modules may eventually read (e.g. a
// future "sales" or "vouchers" module that references the package sold).
type Package struct {
	gorm.Model
	Name       string                    `json:"name"`
	Price      datatypes.JSONType[Price] `json:"price"`
	Duration   string                    `json:"duration"`
	DataLimit  string                    `json:"dataLimit"`
	SpeedLimit string                    `json:"speedLimit"`
	IsActive   bool                      `json:"isActive"`
	UserID     *uint                     `json:"userId"`
	User       *shared.User              `json:"user" gorm:"foreignKey:UserID"`
}

// PackageRequest is the request body for creating or updating a package.
// Field types mirror the frontend's PackageFormValues exactly (all string
// except IsActive) so the client can send its form state directly.
type PackageRequest struct {
	Name       string `json:"name"`
	Price      string `json:"price"`
	Duration   string `json:"duration"`
	DataLimit  string `json:"dataLimit"`
	SpeedLimit string `json:"speedLimit"`
	IsActive   bool   `json:"isActive"`
}

// PackageResponse is returned for single-package endpoints (create, update,
// find by id).
type PackageResponse struct {
	ID         uint   `json:"ID"`
	Name       string `json:"name"`
	Price      string `json:"price"`
	Duration   string `json:"duration"`
	DataLimit  string `json:"dataLimit"`
	SpeedLimit string `json:"speedLimit"`
	IsActive   bool   `json:"isActive"`
}

// PackageListResponse is returned by the list endpoint.
type PackageListResponse struct {
	Packages []PackageResponse `json:"packages"`
}

// ToggleActiveRequest is the body for the PATCH /packages/:id/active route.
type ToggleActiveRequest struct {
	IsActive bool `json:"isActive"`
}

// DeleteManyRequest is the body for bulk-deleting packages (used by the
// "delete selected" toolbar action on the frontend).
type DeleteManyRequest struct {
	IDs []uint `json:"ids"`
}
