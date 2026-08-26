package auth

import "github.com/kigongo-vincent/hotspot-be/modules/base"

func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("auth")

	g.Post("/signup", s.SignUp)
	g.Post("/login", s.Login)
}
