package payments

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("payments", middleware.VerifyJWT())

	g.Post("/flutterwave/charge", s.Charge)
	g.Get("/flutterwave/verify/:txRef", s.Verify)
}
