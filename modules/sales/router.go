package sales

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	router := r.Router.Group("/sales", middleware.VerifyJWT())

	router.Post("/list", s.List)
	router.Get("/stats", s.Stats)
	router.Post("/", s.Create)
	router.Put("/:id", s.Update)
	router.Delete("/:id", s.Delete)
	router.Post("/bulk-delete", s.BulkDelete)
}
