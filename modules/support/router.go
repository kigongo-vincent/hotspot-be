package support

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("/support", middleware.VerifyJWT())

	g.Get("/overview", s.Overview)
	g.Get("/search", s.Search)
	g.Get("/alerts", s.Alerts)
	g.Get("/stats", s.Stats)
	g.Get("/login-series", s.LoginSeries)
}
