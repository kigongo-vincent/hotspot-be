package test

import (
	"fmt"
	"testing"
	"time"

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
	t.Error()
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
	return time.Now().Unix()
}
