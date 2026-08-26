package auth

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthService struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *AuthService {
	return &AuthService{DB: db}
}

// SignUp creates a user + business, then returns an auth token.
func (s *AuthService) SignUp(c *fiber.Ctx) error {
	var body SignupRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.Email == "" || body.Password == "" || body.Name == "" {
		return base.API_ERROR(c, "name, email and password are required")
	}

	var existing shared.User
	if err := s.DB.Where("email = ?", body.Email).First(&existing).Error; err == nil {
		return base.API_ERROR(c, "an account with this email already exists")
	}

	hashed, hErr := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if hErr != nil {
		return base.API_ERROR(c, "failed to secure password")
	}

	business := shared.Business{
		Name:     body.BusinessName,
		Location: body.BusinessLocation,
	}
	if err := s.DB.Create(&business).Error; err != nil {
		return base.API_ERROR(c, "failed to create business")
	}

	user := shared.User{
		Name:     body.Name,
		Email:    body.Email,
		Phone:    body.Phone,
		Password: string(hashed),
		// BusinessID: &business.ID,
	}
	if err := s.DB.Create(&user).Error; err != nil {
		return base.API_ERROR(c, "failed to create account")
	}

	token, tErr := generateToken(user.ID)
	if tErr != nil {
		return base.API_ERROR(c, "failed to generate token")
	}

	return c.JSON(base.APIResponse{Data: LoginResponse{
		Token: token,
		User: UserPreview{
			ID:    user.ID,
			Name:  user.Name,
			Email: user.Email,
		},
	}})
}

// Login authenticates a user by email + password and returns a token.
func (s *AuthService) Login(c *fiber.Ctx) error {
	var body LoginRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.Email == "" || body.Password == "" {
		return base.API_ERROR(c, "email and password are required")
	}

	var user shared.User
	if err := s.DB.Where("email = ?", body.Email).First(&user).Error; err != nil {
		return base.API_ERROR(c, "invalid email or password")
	}

	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(body.Password)) != nil {
		return base.API_ERROR(c, "invalid email or password")
	}

	token, tErr := generateToken(user.ID)
	if tErr != nil {
		return base.API_ERROR(c, "failed to generate token")
	}

	return c.JSON(base.APIResponse{Data: LoginResponse{
		Token: token,
		User: UserPreview{
			ID:    user.ID,
			Name:  user.Name,
			Email: user.Email,
		},
	}})
}

// generateToken signs a JWT carrying the user's ID, matching the shape
// base.GetUserIDFromCtx expects to find in c.Locals("id").
func generateToken(userID uint) (string, error) {
	claims := jwt.MapClaims{
		"id":  userID,
		"exp": time.Now().Add(time.Hour * 24 * 7).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(base.JWTSecret()))
}
