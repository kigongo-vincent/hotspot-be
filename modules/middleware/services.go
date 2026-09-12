package middleware

import (
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"gorm.io/gorm"
)

// OptionalJWT parses Bearer JWT when present and sets locals "uid" and "role" on success.
// Invalid or missing tokens do not fail the request (for public routes that enrich responses when logged in).
func OptionalJWT() fiber.Handler {
	return func(c *fiber.Ctx) error {
		tokenString := strings.TrimSpace(c.Get("Authorization"))
		if tokenString == "" {
			return c.Next()
		}
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")
		if tokenString == "" {
			return c.Next()
		}
		secret := []byte(os.Getenv("JWT_SECRET"))
		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
			return secret, nil
		})
		if err != nil || !token.Valid {
			return c.Next()
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return c.Next()
		}
		role, ok := claims["role"]
		if !ok {
			return c.Next()
		}
		id, ok := claims["uid"]
		if !ok {
			return c.Next()
		}
		c.Locals("role", role)
		c.Locals("uid", id)
		return c.Next()
	}
}

func VerifyJWT() fiber.Handler {

	return func(c *fiber.Ctx) error {

		// get token from request
		tokenString := c.Get("Authorization")
		if tokenString == "" {
			return c.Status(400).JSON(base.APIResponse{Message: "missing token"})
		}
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")

		secret := []byte(os.Getenv("JWT_SECRET"))
		// verify jwt
		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
			return secret, nil
		})
		if err != nil {
			return c.Status(401).JSON(base.APIResponse{Message: "authentication failed"})
		}
		if !token.Valid {
			return c.Status(401).JSON(base.APIResponse{Message: "authentication failed"})
		}

		claims := token.Claims.(jwt.MapClaims)

		// get the user role and user id
		role, ok := claims["role"]
		if !ok {
			return c.Status(403).JSON(base.APIResponse{Message: "authentication failed"})
		}
		id, ok := claims["uid"]
		if !ok {
			return c.Status(401).JSON(base.APIResponse{Message: "authentication failed"})
		}

		c.Locals("uid", id)
		c.Locals("role", role)

		return c.Next()

	}

}

// RequireActiveUser ensures the JWT user exists and is not deactivated. Use after VerifyJWT.
func RequireActiveUser(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		uidVal := c.Locals("uid")
		if uidVal == nil {
			return c.Status(401).JSON(base.APIResponse{Message: "authentication failed"})
		}
		uid := uint(uidVal.(float64))
		var row struct {
			IsActive bool
		}
		if err := db.Table("users").Select("is_active").Where("id = ? AND deleted_at IS NULL", uid).Take(&row).Error; err != nil {
			return c.Status(401).JSON(base.APIResponse{Message: "authentication failed"})
		}
		if !row.IsActive {
			return c.Status(403).JSON(base.APIResponse{Message: "account is deactivated"})
		}
		return c.Next()
	}
}
