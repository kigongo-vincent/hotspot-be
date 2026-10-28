package base

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type RouteRegister struct {
	DB     *gorm.DB
	Router fiber.Router
}

type PaginationControls struct {
	Limit uint `json:"limit"`
	Page  uint `json:"page"`
	Total uint `json:"total"`
}

type FilterColumn struct {
	Column   string      `json:"column"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

type APIRequest struct {
	Pagination PaginationControls `json:"pagination"`
	Columns    []FilterColumn     `json:"columns"`
	Search     string             `json:"search"`
}

// APIResponse is the single response envelope every endpoint in this
// service returns:
//
//	{ "status": 200, "data": ..., "msg": "...", "pagination": {...} }
//
// The frontend's API client (src/api/index.ts) reads `status`, `data`,
// `msg` and `pagination`, so every handler must produce this shape. Use
// OK / OKPage / APIError (see services.go) rather than hand-rolling a
// fiber.Map so the envelope stays consistent.
type APIResponse struct {
	Status     int                `json:"status"`
	Data       interface{}        `json:"data"`
	Message    string             `json:"msg"`
	Pagination PaginationControls `json:"pagination"`
}

// MarshalJSON guarantees a status code is always present on the wire.
// Handlers that build a response without setting Status (the common
// case for a 200) get 200 rather than a misleading 0.
func (r APIResponse) MarshalJSON() ([]byte, error) {
	type alias APIResponse
	if r.Status == 0 {
		r.Status = fiber.StatusOK
	}
	return json.Marshal(alias(r))
}

// APIResponseT is the generic twin of APIResponse, for handlers that
// know the exact type of their payload.
type APIResponseT[T any] struct {
	Status     int                `json:"status"`
	Data       T                  `json:"data"`
	Message    string             `json:"msg"`
	Pagination PaginationControls `json:"pagination"`
}

func (r APIResponseT[T]) MarshalJSON() ([]byte, error) {
	type alias APIResponseT[T]
	if r.Status == 0 {
		r.Status = fiber.StatusOK
	}
	return json.Marshal(alias(r))
}
