package dashboard

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("/dashboard", middleware.VerifyJWT())
	g.Post("/", s.Index)
	g.Post("/overview", s.OverviewData)
	g.Post("/usage-analytics", s.UsageAnalytics)
}
