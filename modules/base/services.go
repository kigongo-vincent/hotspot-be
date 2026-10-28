package base

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

// API_ERROR returns a 400 response using the standard envelope. It is
// kept for the many call sites that predate APIError; new code should
// prefer APIError with an explicit status code.
func API_ERROR(c *fiber.Ctx, v string) error {
	return APIError(c, fiber.StatusBadRequest, v)
}

// APIError returns an error response with an explicit HTTP status code,
// in the same {status, data, msg, pagination} envelope every other
// endpoint uses. The `status` field always mirrors the HTTP status.
func APIError(c *fiber.Ctx, status int, v string) error {
	return c.Status(status).JSON(APIResponse{Status: status, Message: v})
}

// OK returns a 200 response with the standard envelope.
func OK(c *fiber.Ctx, data interface{}, msg string) error {
	return c.Status(fiber.StatusOK).JSON(APIResponse{Status: fiber.StatusOK, Data: data, Message: msg})
}

// OKPage returns a 200 response with the standard envelope plus
// pagination metadata, for list endpoints.
func OKPage(c *fiber.Ctx, data interface{}, p PaginationControls, msg string) error {
	return c.Status(fiber.StatusOK).JSON(APIResponse{
		Status:     fiber.StatusOK,
		Data:       data,
		Message:    msg,
		Pagination: p,
	})
}

func SetupApplication() *fiber.App {
	godotenv.Load()
	app := fiber.New()
	app.Use(limiter.New(limiter.Config{Max: 100, Expiration: 60 * time.Second}))

	return app
}

func GetUserIDFromCtx(c *fiber.Ctx) (uint, error) {
	// The JWT middleware stores the subject as a float64 (JSON number),
	// so normalise both shapes rather than assuming one.
	switch val := c.Locals("uid").(type) {
	case uint:
		return val, nil
	case float64:
		return uint(val), nil
	case int:
		return uint(val), nil
	case int64:
		return uint(val), nil
	default:
		return 0, errors.New("unauthorized: missing or invalid user token context")
	}
}

// FilteredPagination unwraps the PaginationControls from the APIRequest
// and hands it off to the base Paginate scopes function.
func FilteredPagination(r *APIRequest) func(db *gorm.DB) *gorm.DB {
	if r == nil {
		// Return an empty scope modifier if the request payload is nil
		return func(db *gorm.DB) *gorm.DB { return db }
	}
	return Paginate(&r.Pagination)
}

// in base/ or config/
func GoogleClientID() string {
	return os.Getenv("GOOGLE_CLIENT_ID")
}

// Paginate provides the reusable GORM Scope logic for calculating offset bounds.
func Paginate(p *PaginationControls) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if p == nil {
			return db
		}
		if p.Limit == 0 {
			p.Limit = 10
		}
		if p.Page == 0 {
			p.Page = 1
		}

		offset := (p.Page - 1) * p.Limit
		return db.Offset(int(offset)).Limit(int(p.Limit))
	}
}

func JWTSecret() string {
	return os.Getenv("JWT_SECRET")
}

// ---------------------------------------------------------------------------
// Safe dynamic filtering
// ---------------------------------------------------------------------------

// AllowedFilterOperators lists every operator the dynamic filter helper
// understands. Operators are matched exactly (case-sensitive) and any
// operator outside this set is ignored rather than interpolated.
var AllowedFilterOperators = map[string]bool{
	"=":        true,
	"!=":       true,
	">":        true,
	">=":       true,
	"<":        true,
	"<=":       true,
	"LIKE":     true,
	"is_null":  true,
	"not_null": true,
	"in":       true,
}

// ApplyFilters applies a list of client-supplied filter columns to a
// query.
//
// The column name is NEVER taken verbatim from the payload - it must
// appear in the caller-supplied `allowed` whitelist - and every value is
// bound as a query parameter, never concatenated. Unknown columns and
// unknown operators are skipped so a malformed filter can never widen
// or break the query.
//
// `allowed` maps a column name to the SQL column it refers to, e.g.
// {"status": "status", "sold_at": "sold_at"}. Modules that need
// pseudo-columns (e.g. "trashed") should handle those before calling
// this helper.
func ApplyFilters(q *gorm.DB, columns []FilterColumn, allowed map[string]string) *gorm.DB {
	for _, f := range columns {
		column, ok := allowed[f.Column]
		if !ok || column == "" {
			continue
		}
		if !AllowedFilterOperators[f.Operator] {
			continue
		}

		switch f.Operator {
		case "is_null":
			q = q.Where(fmt.Sprintf("%s IS NULL", column))
		case "not_null":
			q = q.Where(fmt.Sprintf("%s IS NOT NULL", column))
		case "in":
			values, ok := f.Value.([]interface{})
			if !ok || len(values) == 0 {
				continue
			}
			q = q.Where(fmt.Sprintf("%s IN ?", column), values)
		default:
			if f.Value == nil {
				continue
			}
			if s, ok := f.Value.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			q = q.Where(fmt.Sprintf("%s %s ?", column, f.Operator), f.Value)
		}
	}
	return q
}
