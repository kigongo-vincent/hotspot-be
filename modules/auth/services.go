package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

// SignUp creates a user and returns an auth token.
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

	user := shared.User{
		Name:     body.Name,
		Email:    body.Email,
		Phone:    body.Phone,
		Password: string(hashed),
	}

	// save user
	if err := s.DB.Create(&user).Error; err != nil {
		return base.API_ERROR(c, "failed to create account")
	}

	// save company
	tmpCompany := shared.Company{UserID: &user.ID}
	if cErr := s.DB.Create(&tmpCompany).Error; cErr != nil {
		return base.API_ERROR(c, "failed to create a company for the user")
	}

	token, tErr := GenerateToken(user.ID, user.Role)
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

	token, tErr := GenerateToken(user.ID, user.Role)
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

// googleUserInfo is the subset of fields Google's userinfo endpoint returns
// that this handler actually needs.
type googleUserInfo struct {
	Sub           string `json:"sub"` // Google's stable per-account ID — store this in User.GoogleID, not email, since email can change on the Google account itself.
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// verifyGoogleAccessToken calls Google's userinfo endpoint with the given
// OAuth access token. This is the access-token equivalent of verifying an
// ID token's signature: if Google's server accepts the token and returns
// the profile, the token is real and was actually issued by Google for
// this user — a forged/expired/revoked token gets a 401 from Google and
// an error here. No client secret is involved; this is a plain
// authenticated GET using the token itself as the credential.
func verifyGoogleAccessToken(accessToken string) (*googleUserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+url.QueryEscape(accessToken))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google userinfo returned status %d", resp.StatusCode)
	}

	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

// GoogleAuth verifies a Google OAuth access token (obtained by the frontend
// via a plain redirect to Google's consent screen — the implicit grant,
// response_type=token) against Google's own userinfo endpoint, then finds
// or creates the corresponding user and returns a normal session token.
// No client secret or authorization-code exchange is ever involved.
func (s *AuthService) GoogleAuth(c *fiber.Ctx) error {
	var body GoogleAuthRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.AccessToken == "" {
		return base.API_ERROR(c, "missing google access token")
	}

	info, vErr := verifyGoogleAccessToken(body.AccessToken)
	if vErr != nil {
		return base.API_ERROR(c, "invalid google access token")
	}

	if info.Email == "" {
		return base.API_ERROR(c, "google account has no email")
	}

	if !info.EmailVerified {
		return base.API_ERROR(c, "google email is not verified")
	}

	var user shared.User
	err := s.DB.Where("email = ?", info.Email).First(&user).Error

	if err != nil {
		// No existing account — provision one. GoogleID marks this as a
		// Google-originated account; Password stays empty so a normal
		// email/password Login can never match it via bcrypt.
		user = shared.User{
			Name:     info.Name,
			Email:    info.Email,
			GoogleID: info.Sub,
			IsActive: true,
		}
		if cErr := s.DB.Create(&user).Error; cErr != nil {
			return base.API_ERROR(c, "failed to create account")
		}

		// save company
		tmpCompany := shared.Company{UserID: &user.ID}
		if coErr := s.DB.Create(&tmpCompany).Error; coErr != nil {
			return base.API_ERROR(c, "failed to create a company for the user")
		}

	} else if user.GoogleID == "" {
		// Existing email/password account signing in with Google for the
		// first time — link the two rather than creating a duplicate row.
		user.GoogleID = info.Sub
		if uErr := s.DB.Save(&user).Error; uErr != nil {
			return base.API_ERROR(c, "failed to link google account")
		}
	}

	token, tErr := GenerateToken(user.ID, user.Role)
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

// GenerateToken signs a JWT carrying the user's ID, matching the shape
// base.GetUserIDFromCtx expects to find in c.Locals("id").
func GenerateToken(userID uint, role shared.UserRole) (string, error) {
	claims := jwt.MapClaims{
		"uid":  userID,
		"role": role,
		"exp":  time.Now().Add(time.Hour * 24 * 7).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(base.JWTSecret()))
}
