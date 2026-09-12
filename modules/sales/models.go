package sales

import (
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"
)

// Sale is not a distinct table. "Selling" a voucher means taking an
// existing PROVISIONED vouchers.Voucher row and transitioning it to USED
// with payer/channel attribution attached. This module reads and writes
// the vouchers table owned by the voucher module — it does not create a
// parallel schema.
//
// IMPORTANT — schema change required in the voucher module:
// vouchers.Voucher currently has no fields to record who paid, through
// which channel, or when. Add these four fields to vouchers.Voucher
// directly (not duplicated here):
//
//	Channel   string  `json:"channel"`
//	Payer     string  `json:"payer"`
//	PayerName string  `json:"payerName"`
//	SoldAt    *string `json:"soldAt"`
//
// Once added, run a migration (`db.AutoMigrate(&vouchers.Voucher{})`) and
// remove this comment block. Everything below assumes those fields exist.
type Sale = vouchers.Voucher

// CreateSaleRequest is the payload for POST /api/sales — this is what a
// payment-success handler (or manual free-allocation flow) submits to
// convert a provisioned voucher into a sold one.
type CreateSaleRequest struct {
	// VoucherCode is optional: if empty, the service assigns the oldest
	// available PROVISIONED voucher matching PackageID.
	VoucherCode string `json:"voucherCode"`
	PackageID   uint   `json:"packageId"`
	Channel     string `json:"channel"`
	Payer       string `json:"payer"`     // blank for free allocations
	PayerName   string `json:"payerName"` // blank for free allocations
	UserID      *uint  `json:"userId"`
}

type FilterColumn struct {
	Column   string      `json:"column"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

type PaginationParams struct {
	Limit int `json:"limit"`
	Page  int `json:"page"`
	Total int `json:"total"`
}

type SaleListRequest struct {
	Pagination PaginationParams `json:"pagination"`
	Columns    []FilterColumn   `json:"columns"`
	Search     string           `json:"search"`
}

type BulkDeleteRequest struct {
	IDs []uint `json:"ids"`
}

// RevenueByCurrency holds a summed total for one currency. Packages could
// in principle be priced in more than one Currency (only UGX exists today),
// so revenue is grouped rather than assumed to be a single number.
type RevenueByCurrency struct {
	Currency packages.Currency `json:"currency"`
	Total    int               `json:"total"`
}

// DailySales is one point in the trailing 7-day sales-count series.
type DailySales struct {
	Date  string `json:"date"`  // YYYY-MM-DD
	Sales int64  `json:"sales"` // count of vouchers sold (status=USED) on that date
}

// SaleStats is returned by GET /api/sales/stats
type SaleStats struct {
	TotalSales int64               `json:"totalSales"`
	PaidSales  int64               `json:"paidSales"`
	FreeSales  int64               `json:"freeSales"`
	Revenue    []RevenueByCurrency `json:"revenue"`
	// UnpricedSales counts paid sales whose package could not be resolved
	// (e.g. the package was deleted after the sale). Revenue excludes
	// these — a nonzero count here means revenue is understated.
	UnpricedSales int64 `json:"unpricedSales"`
	// Daily is the trailing 7-day sales count, oldest first, for the
	// performance chart. Every day in the range is present even if 0.
	Daily []DailySales `json:"daily"`
}
