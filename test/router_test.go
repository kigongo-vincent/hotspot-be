package test

import (
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/router"
)

func TestCreateRouter(t *testing.T) {
	t.Skip()
	HTTP[any, router.CreateRouterRequest](HTTPRequest[any, router.CreateRouterRequest]{Method: fiber.MethodPost, Path: "router", Body: router.CreateRouterRequest{
		Name: "FOG 7 WIFI",
	}})
	t.Error()
}

func TestCreateVPN(t *testing.T) {
	t.Skip()
	var RouterID uint = 1
	HTTP[any, router.CreateVPNRequest](HTTPRequest[any, router.CreateVPNRequest]{Method: fiber.MethodPost, Path: "vpn", Body: router.CreateVPNRequest{RouterID: &RouterID}})
	t.Error()
}
func TestHasRouter(t *testing.T) {
	t.Skip()
	HTTP[any, any](HTTPRequest[any, any]{Method: fiber.MethodGet, Path: "router/check"})
}

func TestGetDashboard(t *testing.T) {
	_, _, e := HTTP(HTTPRequest[any, any]{Method: fiber.MethodGet, Path: "router/dashboard"})
	t.Error(e)
}
