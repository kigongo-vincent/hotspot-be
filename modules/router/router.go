package router

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	router := r.Router.Group("/router", middleware.VerifyJWT())
	router.Post("/", s.CreateRouter)
	router.Get("/check", s.CheckStatus)
	router.Get("/dashboard", s.GetDashboard)

	// Router inventory management.
	router.Get("/list", s.List)
	router.Get("/:id", s.FindByID)
	router.Put("/:id", s.Update)
	router.Delete("/:id", s.Delete)

	// MikroTik management: hotspot users, credential rotation and
	// remote access.
	router.Get("/:id/hotspot/users", s.ListHotspotUsersHandler)
	router.Post("/:id/hotspot/users", s.ProvisionHotspotUserHandler)
	router.Delete("/:id/hotspot/users/:username", s.RemoveHotspotUserHandler)
	router.Post("/:id/rotate-credentials", s.RotateCredentials)
	router.Post("/:id/rotate-credentials/confirm", s.ConfirmRotation)
	router.Get("/:id/remote-access", s.GetRemoteAccessHandler)
	router.Post("/:id/remote-access", s.SetRemoteAccessHandler)

	router.Post("/cmd", s.cmd)
	// Hit by the MikroTik itself using the per-router bearer token minted
	// in GenerateBootstrapCommand - VerifyJWT() decodes that token the same
	// way it decodes a user token, it just carries a router ID as subject.
	// :routerId in the path is cosmetic only (readable URLs/logs) - the
	// actual router identity always comes from the token, in GetVPNScript,
	// so a MikroTik can never fetch a different router's script by editing
	// the URL.
	router.Get("/vpn/script/:routerId", s.GetVPNScript)
	router.Post("/vpn/complete", s.CompleteVPN)
}
