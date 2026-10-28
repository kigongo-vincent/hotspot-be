package middleware

import (
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/gorm"
)

// signingMethods pins the accepted JWT signing algorithm. Without this,
// the classic "alg" confusion attack (an attacker re-signing the token
// with "none", or with RS256 using the HMAC secret as the public key)
// becomes possible.
var signingMethods = []string{"HS256"}

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
		}, jwt.WithValidMethods(signingMethods))
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
		tokenString := strings.TrimSpace(c.Get("Authorization"))
		if tokenString == "" {
			return base.APIError(c, fiber.StatusUnauthorized, "missing token")
		}
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")
		if tokenString == "" {
			return base.APIError(c, fiber.StatusUnauthorized, "missing token")
		}

		secret := []byte(os.Getenv("JWT_SECRET"))
		// verify jwt
		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
			return secret, nil
		}, jwt.WithValidMethods(signingMethods))
		if err != nil {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}
		if !token.Valid {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}

		// get the user role and user id
		role, ok := claims["role"]
		if !ok {
			return base.APIError(c, fiber.StatusForbidden, "authentication failed")
		}
		id, ok := claims["uid"]
		if !ok {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}

		c.Locals("uid", id)
		c.Locals("role", role)

		return c.Next()

	}

}

// RequireActiveUser ensures the JWT user exists and is not deactivated. Use after VerifyJWT.
func RequireActiveUser(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		uid, err := base.GetUserIDFromCtx(c)
		if err != nil {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}
		var row struct {
			IsActive bool
		}
		if err := db.Table("users").Select("is_active").Where("id = ? AND deleted_at IS NULL", uid).Take(&row).Error; err != nil {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}
		if !row.IsActive {
			return base.APIError(c, fiber.StatusForbidden, "account is deactivated")
		}
		return c.Next()
	}
}

// RequireRole restricts a route to the listed roles. It must run after
// VerifyJWT, which is what populates the "role" local.
func RequireRole(roles ...shared.UserRole) fiber.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[string(r)] = true
	}

	return func(c *fiber.Ctx) error {
		role, ok := c.Locals("role").(string)
		if !ok {
			return base.APIError(c, fiber.StatusUnauthorized, "authentication failed")
		}
		if !allowed[role] {
			return base.APIError(c, fiber.StatusForbidden, "insufficient permissions")
		}
		return c.Next()
	}
}
