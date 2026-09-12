package users

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("users", middleware.VerifyJWT())

	g.Get("/", s.FindAll)
	g.Get("/:id", s.FindByID)
	g.Post("/", s.Create)
	g.Put("/:id", s.Update)
	g.Post("/bulk-delete", s.DeleteMany)
	g.Delete("/:id", s.Delete)
}
