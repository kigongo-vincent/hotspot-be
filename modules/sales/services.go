package sales

import (
	"errors"
	"fmt"
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// allowedFilterColumns whitelists which columns can be used in dynamic
// WHERE clauses, to prevent injection via the "column" field of the payload.
var allowedFilterColumns = map[string]bool{
	"channel":    true,
	"package_id": true,
	"sold_at":    true,
	"payer":      true,
	"username":   true,
	"agent_id":   true,
}

var allowedOperators = map[string]bool{
	"=": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true, "LIKE": true,
}

var ErrNoVoucherAvailable = errors.New("no provisioned voucher available for this package")

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// ---------------------------------------------------------------------
// Route-facing methods (registered directly on the Fiber router, same
// pattern as the router module: no separate handler layer).
// ---------------------------------------------------------------------

// List handles POST /sales/list.
func (s *Service) List(c *fiber.Ctx) error {
	var req SaleListRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid payload"})
	}

	sales, total, err := s.listSales(req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}

	limit := req.Pagination.Limit
	if limit <= 0 {
		limit = 10
	}
	page := req.Pagination.Page
	if page <= 0 {
		page = 1
	}

	return c.JSON(fiber.Map{
		"status": 200,
		"msg":    "success",
		"data":   fiber.Map{"sales": sales},
		"pagination": fiber.Map{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

// Stats handles GET /sales/stats.
func (s *Service) Stats(c *fiber.Ctx) error {
	stats, err := s.getStats()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"status": 200, "msg": "success", "data": stats})
}

// Create handles POST /sales — sells a voucher (assigns a PROVISIONED
// voucher by code or package and marks it USED with payer/channel
// attribution). Typically called from a payment-success webhook, or with
// Payer/PayerName blank for a manual free allocation.
func (s *Service) Create(c *fiber.Ctx) error {
	var req CreateSaleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid payload"})
	}
	if req.VoucherCode == "" && req.PackageID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "voucherCode or packageId is required"})
	}

	sale, err := s.createSale(req)
	if err != nil {
		if errors.Is(err, ErrNoVoucherAvailable) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"status": 409, "msg": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"status": 200, "msg": "sold", "data": sale})
}

// Update handles PUT /sales/:id.
func (s *Service) Update(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid ID"})
	}
	var updates Sale
	if err := c.BodyParser(&updates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid payload"})
	}
	sale, err := s.updateSale(uint(id), &updates)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"status": 200, "msg": "updated", "data": sale})
}

// Delete handles DELETE /sales/:id.
func (s *Service) Delete(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid ID"})
	}
	if err := s.deleteSale(uint(id)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"status": 200, "msg": "deleted"})
}

// BulkDelete handles POST /sales/bulk-delete.
func (s *Service) BulkDelete(c *fiber.Ctx) error {
	var req BulkDeleteRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"status": 400, "msg": "Invalid payload"})
	}
	if err := s.bulkDeleteSales(req.IDs); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"status": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"status": 200, "msg": "bulk deleted successfully"})
}

// ---------------------------------------------------------------------
// Private business logic (pure of Fiber, easy to unit test directly).
// ---------------------------------------------------------------------

// listSales returns vouchers that have been sold (status = USED), i.e. the
// "sales" record set, with dynamic filters/search/pagination layered on top.
func (s *Service) listSales(req SaleListRequest) ([]Sale, int64, error) {
	var sales []Sale
	var total int64

	query := s.db.Model(&vouchers.Voucher{}).
		Preload("Package").
		Preload("User").
		Preload("Agent")

	if findTrashedFlag(req.Columns) {
		query = query.Unscoped().Where("deleted_at IS NOT NULL")
	} else {
		// Default scope for "sales": only USED vouchers count as sold.
		query = query.Where("status = ?", vouchers.VoucherUsed)
	}

	for _, filter := range req.Columns {
		if filter.Column == "trashed" || filter.Column == "free" {
			continue // handled separately
		}
		if !allowedFilterColumns[filter.Column] || !allowedOperators[filter.Operator] {
			continue
		}
		query = query.Where(fmt.Sprintf("%s %s ?", filter.Column, filter.Operator), filter.Value)
	}

	if hasFreeFlag(req.Columns) {
		query = query.Where("payer = ? OR payer IS NULL", "")
	}

	if req.Search != "" {
		term := "%" + req.Search + "%"
		query = query.Where(
			"username LIKE ? OR payer LIKE ? OR payer_name LIKE ? OR channel LIKE ?",
			term, term, term, term,
		)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := req.Pagination.Limit
	if limit <= 0 {
		limit = 10
	}
	page := req.Pagination.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	err := query.Limit(limit).Offset(offset).Order("created_at desc").Find(&sales).Error
	return sales, total, err
}

func findTrashedFlag(cols []FilterColumn) bool {
	for _, c := range cols {
		if c.Column == "trashed" {
			if b, ok := c.Value.(bool); ok {
				return b
			}
		}
	}
	return false
}

func hasFreeFlag(cols []FilterColumn) bool {
	for _, c := range cols {
		if c.Column == "free" {
			if b, ok := c.Value.(bool); ok {
				return b
			}
		}
	}
	return false
}

func (s *Service) getStats() (*SaleStats, error) {
	var stats SaleStats

	if err := s.db.Model(&vouchers.Voucher{}).
		Where("status = ?", vouchers.VoucherUsed).
		Count(&stats.TotalSales).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&vouchers.Voucher{}).
		Where("status = ? AND payer != ''", vouchers.VoucherUsed).
		Count(&stats.PaidSales).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&vouchers.Voucher{}).
		Where("status = ? AND (payer = '' OR payer IS NULL)", vouchers.VoucherUsed).
		Count(&stats.FreeSales).Error; err != nil {
		return nil, err
	}

	// Price is a structured embedded value (Value int, Currency), so
	// revenue is summed directly in Go. Grouped by currency since packages
	// could in principle use more than one (only UGX exists today).
	var paidSales []vouchers.Voucher
	if err := s.db.Model(&vouchers.Voucher{}).
		Preload("Package").
		Where("status = ? AND payer != ''", vouchers.VoucherUsed).
		Find(&paidSales).Error; err != nil {
		return nil, err
	}

	totals := map[packages.Currency]int{}
	for _, v := range paidSales {
		if v.Package == nil {
			stats.UnpricedSales++ // voucher sold but package record missing/deleted
			continue
		}
		// totals[v.Package.Price.Currency] += v.Package.Price.Value
	}
	for currency, total := range totals {
		stats.Revenue = append(stats.Revenue, RevenueByCurrency{Currency: currency, Total: total})
	}

	daily, err := s.getDailySales()
	if err != nil {
		return nil, err
	}
	stats.Daily = daily

	return &stats, nil
}

// getDailySales returns a sales count per day for the trailing 7 days
// (including today), oldest first. Days with zero sales are included
// explicitly so the chart doesn't silently drop gaps.
func (s *Service) getDailySales() ([]DailySales, error) {
	const days = 7
	today := time.Now()
	start := today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	type row struct {
		SoldAt string
		Count  int64
	}
	var rows []row

	if err := s.db.Model(&vouchers.Voucher{}).
		Select("sold_at, count(*) as count").
		Where("status = ? AND sold_at >= ?", vouchers.VoucherUsed, start).
		Group("sold_at").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.SoldAt] = r.Count
	}

	series := make([]DailySales, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		series = append(series, DailySales{Date: date, Sales: counts[date]})
	}

	return series, nil
}

// createSale is the core "sell a voucher" operation, invoked from a
// payment-success callback (paid sale) or a manual free-allocation flow
// (Payer/PayerName left blank). It atomically:
//  1. Row-locks and selects a PROVISIONED voucher (a specific code, or the
//     oldest one available for the requested package),
//  2. Stamps it with payer/channel/soldAt,
//  3. Transitions its status to USED.
//
// This never inserts a new voucher row — vouchers are provisioned ahead of
// time by the voucher module; selling only consumes an existing one.
func (s *Service) createSale(req CreateSaleRequest) (*Sale, error) {
	var sale Sale

	err := s.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&vouchers.Voucher{}).
			Clauses(clause.Locking{Strength: "UPDATE"}). // prevent two sales grabbing the same voucher
			Where("status = ?", vouchers.VoucherProvisioned)

		if req.VoucherCode != "" {
			q = q.Where("username = ?", req.VoucherCode)
		} else {
			q = q.Where("package_id = ?", req.PackageID)
		}

		if err := q.Order("created_at asc").First(&sale).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoVoucherAvailable
			}
			return err
		}

		now := time.Now().Format("2006-01-02")
		updates := map[string]interface{}{
			"status":     vouchers.VoucherUsed,
			"channel":    req.Channel,
			"payer":      req.Payer,
			"payer_name": req.PayerName,
			"sold_at":    now,
		}
		if req.UserID != nil {
			updates["user_id"] = *req.UserID
		}

		if err := tx.Model(&sale).Updates(updates).Error; err != nil {
			return err
		}
		return tx.First(&sale, sale.ID).Error
	})

	if err != nil {
		return nil, err
	}
	return &sale, nil
}

func (s *Service) updateSale(id uint, updates *Sale) (*Sale, error) {
	var existing Sale
	if err := s.db.First(&existing, id).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

// deleteSale soft-deletes the underlying voucher row. Use with care: this
// removes it from the voucher module's view too, since both modules share
// the same table.
func (s *Service) deleteSale(id uint) error {
	return s.db.Delete(&vouchers.Voucher{}, id).Error
}

func (s *Service) bulkDeleteSales(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.Delete(&vouchers.Voucher{}, ids).Error
}
