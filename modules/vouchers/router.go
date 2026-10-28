package vouchers

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("vouchers", middleware.VerifyJWT())

	g.Get("/", s.FindAll)
	g.Post("/list", s.ListVouchers)
	g.Get("/stats", s.GetVoucherStats)
	g.Get("/batches", s.GetVoucherBatches)
	g.Get("/batches/:note", s.GetVoucherBatch)
	g.Get("/:id", s.FindByID)
	g.Post("/", s.Create)
	g.Put("/:id", s.Update)
	g.Post("/bulk-delete", s.DeleteMany)
	g.Delete("/:id", s.Delete)
	g.Post("/restore/:id", s.Restore)
	g.Post("/bulk-restore", s.RestoreMany)
}
