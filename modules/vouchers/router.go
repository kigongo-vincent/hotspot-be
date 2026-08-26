package vouchers

import "github.com/kigongo-vincent/hotspot-be/modules/base"

func RouteRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("/vouchers")
	g.Post("/", s.Create)
}
