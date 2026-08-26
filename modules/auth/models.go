package auth

// SignupRequest is the payload for POST auth/signup.
type SignupRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	Password         string `json:"password"`
	BusinessName     string `json:"businessName"`
	BusinessLocation string `json:"businessLocation"`
}

// LoginRequest is the payload for POST auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is returned by both signup and login on success.
type LoginResponse struct {
	Token string      `json:"token"`
	User  UserPreview `json:"user"`
}

// UserPreview is the trimmed-down user shape sent back to the client.
// Kept module-private/dumb — full shared.User lives in shared/.
type UserPreview struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
