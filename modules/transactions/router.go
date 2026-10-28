package transactions

import (
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/middleware"
)

// RouterRegister wires up read-only routes only. There is deliberately no
// POST/PUT/DELETE here: transactions are an append-only ledger, written
// to exclusively via the internal CreateTransaction(...) helper in
// services.go, called by other modules (Vouchers, Sales, ...) — never by
// a client request.
func RouterRegister(r *base.RouteRegister) {
	s := NewService(r.DB)
	g := r.Router.Group("transactions", middleware.VerifyJWT())

	g.Post("/list", s.ListTransactions)
	g.Get("/stats", s.GetStats)
	g.Get("/", s.FindAll)
	g.Get("/:id", s.FindByID)
}
