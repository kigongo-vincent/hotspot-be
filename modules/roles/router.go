package roles

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("/roles", middleware.VerifyJWT())

	g.Get("/list", s.List)
	g.Get("/catalogue", s.Catalogue)
	g.Get("/:id", s.FindByID)
	g.Post("/", s.Create)
	g.Put("/:id", s.Update)
	g.Delete("/:id", s.Delete)
	g.Post("/bulk-delete", s.DeleteMany)
}
