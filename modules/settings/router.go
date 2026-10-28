package settings

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("/settings", middleware.VerifyJWT())

	g.Get("/", s.List)
	g.Get("/catalogue", s.Catalogue)
	g.Put("/", s.Upsert)
	g.Post("/bulk", s.BulkUpsert)
	g.Delete("/:key", s.Delete)
}
