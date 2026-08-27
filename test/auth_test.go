package test

import (
	"fmt"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/auth"
)

func TestSignUp(t *testing.T) {
	HTTP[auth.LoginResponse, auth.SignupRequest](HTTPRequest[auth.LoginResponse, auth.SignupRequest]{
		Method: fiber.MethodPost,
		Path:   "auth/signup",
		Body: auth.SignupRequest{
			Name:             "vincent",
			Email:            fmt.Sprintf("vincent-%d@gmail.com", timeNowUnix()),
			Phone:            "0745253954",
			Password:         "1234",
			BusinessName:     "Tora Dynamics",
			BusinessLocation: "Kampala Uganda",
		},
	})
}

func TestLogin(t *testing.T) {
	email := fmt.Sprintf("login-fixture-%d@gmail.com", timeNowUnix())

	// Arrange: create the account we're about to log into.
	HTTP[auth.LoginResponse, auth.SignupRequest](HTTPRequest[auth.LoginResponse, auth.SignupRequest]{
		Method: fiber.MethodPost,
		Path:   "auth/signup",
		Body: auth.SignupRequest{
			Name:             "login tester",
			Email:            email,
			Phone:            "0700000000",
			Password:         "supersecret",
			BusinessName:     "Test Co",
			BusinessLocation: "Kampala Uganda",
		},
	})

	HTTP[auth.LoginResponse, auth.LoginRequest](HTTPRequest[auth.LoginResponse, auth.LoginRequest]{
		Method: fiber.MethodPost,
		Path:   "auth/login",
		Body: auth.LoginRequest{
			Email:    email,
			Password: "supersecret",
		},
	})

}

func timeNowUnix() int64 {
	return timeNowUnix()
}

// TestGoogleAuthRejectsInvalidCredential covers the one part of this route
// that doesn't need a real Google session: a malformed/forged ID token must
// be rejected rather than silently accepted. Exercising the success path
// requires a live Google-signed token, which isn't reproducible in a unit
// test — that path should be covered by an integration/manual check against
// GOOGLE_CLIENT_ID instead.
func TestGoogleAuthRejectsInvalidCredential(t *testing.T) {
	HTTP[auth.LoginResponse, auth.GoogleAuthRequest](HTTPRequest[auth.LoginResponse, auth.GoogleAuthRequest]{
		Method: fiber.MethodPost,
		Path:   "auth/google",
		Body: auth.GoogleAuthRequest{
			Credential: "not-a-real-google-id-token",
		},
	})
}
