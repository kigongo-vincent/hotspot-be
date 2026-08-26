package base

import (
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

type APIResponse struct {
	Data       interface{}        `json:"data"`
	Message    string             `json:"msg"`
	Pagination PaginationControls `json:"pagination"`
}

type APIResponseT[T any] struct {
	Status     int                `json:"status"`
	Data       T                  `json:"data"`
	Message    string             `json:"msg"`
	Pagination PaginationControls `json:"pagination"`
}
