package packages

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("packages", middleware.VerifyJWT())

	g.Get("/", s.FindAll)
	g.Get("/:id", s.FindByID)
	g.Post("/", s.Create)
	g.Put("/:id", s.Update)
	g.Put("/:id/active", s.ToggleActive)
	g.Post("/bulk-delete", s.DeleteMany)
	g.Delete("/:id", s.Delete)
}
