package disbursements

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("disbursements", middleware.VerifyJWT())

	g.Post("/list", s.ListDisbursements)
	g.Get("/stats", s.GetStats)
	g.Get("/", s.FindAll)
	g.Post("/", s.CreateDisbursement)
	g.Get("/:id", s.FindByID)
}
