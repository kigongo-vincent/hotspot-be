package sales

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/router"
	"github.com/kigongo-vincent/hotspot-be/modules/settings"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// allowedFilterColumns whitelists which columns can be used in dynamic
// WHERE clauses, to prevent injection via the "column" field of the payload.
// The value is the SQL column the filter applies to.
var allowedFilterColumns = map[string]string{
	"channel":    "channel",
	"package_id": "package_id",
	"status":     "status",
	"sold_at":    "sold_at",
	"payer":      "payer",
	"payer_name": "payer_name",
	"username":   "username",
	"agent_id":   "agent_id",
	"router_id":  "router_id",
}

var ErrNoVoucherAvailable = errors.New("no provisioned voucher available for this package")
var ErrSaleNotFound = errors.New("sale not found")

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
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var req SaleListRequest
	if err := c.BodyParser(&req); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	sales, total, err := s.listSales(userID, req)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	limit := req.Pagination.Limit
	if limit <= 0 {
		limit = 10
	}
	page := req.Pagination.Page
	if page <= 0 {
		page = 1
	}

	return base.OKPage(c, SaleListResponse{Sales: sales}, base.PaginationControls{
		Limit: uint(limit),
		Page:  uint(page),
		Total: uint(total),
	}, "sales fetched")
}

// Stats handles GET /sales/stats.
func (s *Service) Stats(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	stats, err := s.getStats(userID)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, stats, "sales stats fetched")
}

// Create handles POST /sales — sells a voucher (assigns a PROVISIONED
// voucher by code or package and marks it USED with payer/channel
// attribution). Typically called from a payment-success webhook, or with
// Payer/PayerName blank for a manual free allocation.
func (s *Service) Create(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var req CreateSaleRequest
	if err := c.BodyParser(&req); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if req.VoucherCode == "" && req.PackageID == 0 {
		return base.APIError(c, fiber.StatusBadRequest, "voucherCode or packageId is required")
	}

	sale, err := s.createSale(userID, req)
	if err != nil {
		if errors.Is(err, ErrNoVoucherAvailable) {
			return base.APIError(c, fiber.StatusConflict, err.Error())
		}
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	// Provision the sold voucher on the target router, when requested and
	// enabled. A provisioning failure never invalidates the sale - the
	// code is already sold and the operator can retry from the router
	// page - but it is reported back so it is not silently lost.
	provisioned := false
	var provisionMessage string
	if req.RouterID != nil && *req.RouterID != 0 {
		if s.autoProvisionEnabled(userID, req.AutoProvision) {
			if err := s.provisionOnRouter(userID, *req.RouterID, sale); err != nil {
				provisionMessage = err.Error()
			} else {
				provisioned = true
			}
		}
	}

	return base.OK(c, map[string]interface{}{
		"sale":              sale,
		"provisioned":       provisioned,
		"provisionMessage":  provisionMessage,
	}, "voucher sold")
}

// Update handles PUT /sales/:id.
func (s *Service) Update(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	id, err := c.ParamsInt("id")
	if err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "invalid id")
	}

	var body UpdateSaleRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	sale, err := s.updateSale(userID, uint(id), body)
	if err != nil {
		if errors.Is(err, ErrSaleNotFound) {
			return base.APIError(c, fiber.StatusNotFound, err.Error())
		}
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, SaleResponse{Sale: *sale}, "sale updated")
}

// Delete handles DELETE /sales/:id.
func (s *Service) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	id, err := c.ParamsInt("id")
	if err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "invalid id")
	}

	if err := s.deleteSale(userID, uint(id)); err != nil {
		if errors.Is(err, ErrSaleNotFound) {
			return base.APIError(c, fiber.StatusNotFound, err.Error())
		}
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, nil, "sale deleted")
}

// BulkDelete handles POST /sales/bulk-delete.
func (s *Service) BulkDelete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var req BulkDeleteRequest
	if err := c.BodyParser(&req); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if len(req.IDs) == 0 {
		return base.APIError(c, fiber.StatusBadRequest, "no ids provided")
	}

	if err := s.bulkDeleteSales(userID, req.IDs); err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, nil, "sales deleted")
}

// Restore handles POST /sales/restore/:id - brings a soft-deleted sale
// back from the trash.
func (s *Service) Restore(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	id, err := c.ParamsInt("id")
	if err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "invalid id")
	}

	if err := s.restoreSale(userID, uint(id)); err != nil {
		if errors.Is(err, ErrSaleNotFound) {
			return base.APIError(c, fiber.StatusNotFound, err.Error())
		}
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, nil, "sale restored")
}

// BulkRestore handles POST /sales/bulk-restore.
func (s *Service) BulkRestore(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var req BulkDeleteRequest
	if err := c.BodyParser(&req); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if len(req.IDs) == 0 {
		return base.APIError(c, fiber.StatusBadRequest, "no ids provided")
	}

	if err := s.bulkRestoreSales(userID, req.IDs); err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, nil, "sales restored")
}

// ---------------------------------------------------------------------
// Private business logic (pure of Fiber, easy to unit test directly).
// ---------------------------------------------------------------------

// listSales returns vouchers that have been sold (status = USED), i.e. the
// "sales" record set, with dynamic filters/search/pagination layered on top.
// Every row is scoped to the authenticated user.
func (s *Service) listSales(userID uint, req SaleListRequest) ([]Sale, int64, error) {
	var sales []Sale
	var total int64

	query := s.db.Model(&vouchers.Voucher{}).
		Preload("Package").
		Preload("Agent").
		Where("user_id = ?", userID)

	if findTrashedFlag(req.Columns) {
		query = query.Unscoped().Where("deleted_at IS NOT NULL")
	} else {
		// Default scope for "sales": only USED vouchers count as sold.
		query = query.Where("status = ?", vouchers.VoucherUsed)
	}

	query = base.ApplyFilters(query, req.Columns, allowedFilterColumns)

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

	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
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

	err := query.Limit(limit).Offset(offset).Order("sold_at desc nulls last, id desc").Find(&sales).Error
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

func (s *Service) getStats(userID uint) (*SaleStats, error) {
	var stats SaleStats

	if err := s.db.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND status = ?", userID, vouchers.VoucherUsed).
		Count(&stats.TotalSales).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND status = ? AND payer != ''", userID, vouchers.VoucherUsed).
		Count(&stats.PaidSales).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND status = ? AND (payer = '' OR payer IS NULL)", userID, vouchers.VoucherUsed).
		Count(&stats.FreeSales).Error; err != nil {
		return nil, err
	}

	// Price is a structured embedded value (Value int, Currency), so
	// revenue is summed directly in Go. Grouped by currency since packages
	// could in principle use more than one (only UGX exists today).
	var soldSales []vouchers.Voucher
	if err := s.db.Model(&vouchers.Voucher{}).
		Preload("Package").
		Where("user_id = ? AND status = ?", userID, vouchers.VoucherUsed).
		Find(&soldSales).Error; err != nil {
		return nil, err
	}

	totals := map[packages.Currency]int{}
	for _, v := range soldSales {
		if v.Package == nil {
			stats.UnpricedSales++ // voucher sold but package record missing/deleted
			continue
		}
		price := v.Package.Price.Data()
		totals[price.Currency] += price.Value
	}
	stats.Revenue = make([]RevenueByCurrency, 0, len(totals))
	for currency, total := range totals {
		stats.Revenue = append(stats.Revenue, RevenueByCurrency{Currency: currency, Total: total})
	}

	daily, err := s.getDailySales(userID)
	if err != nil {
		return nil, err
	}
	stats.Daily = daily

	return &stats, nil
}

// getDailySales returns a sales count per day for the trailing 7 days
// (including today), oldest first. Days with zero sales are included
// explicitly so the chart doesn't silently drop gaps.
func (s *Service) getDailySales(userID uint) ([]DailySales, error) {
	const days = 7
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))

	// The sales graph is company-scoped: aggregate sales across the owner
	// and every member of the company owned by the logged-in user.
	userIDs := s.companyUserIDs(userID)

	type row struct {
		Date  time.Time
		Count int64
	}
	var rows []row

	if err := s.db.Model(&vouchers.Voucher{}).
		Select("DATE(sold_at) as date, count(*) as count").
		Where("user_id IN ? AND status = ? AND sold_at >= ?", userIDs, vouchers.VoucherUsed, start).
		Group("DATE(sold_at)").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.Date.Format("2006-01-02")] = r.Count
	}

	series := make([]DailySales, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		series = append(series, DailySales{Date: date, Sales: counts[date]})
	}

	return series, nil
}

// createSale is the core "sell a voucher" operation, invoked from a
// payment-success callback (paid sale) or a manual free-allocation flow
// (Payer/PayerName left blank). It atomically:
//  1. Row-locks and selects a PROVISIONED voucher owned by the caller
//     (a specific code, or the oldest one available for the requested
//     package),
//  2. Stamps it with payer/channel/soldAt,
//  3. Transitions its status to USED.
//
// This never inserts a new voucher row — vouchers are provisioned ahead of
// time by the voucher module; selling only consumes an existing one.
//
// The voucher is always stamped with the caller's expiry window so the
// package's duration is honoured the moment the code is sold.
func (s *Service) createSale(userID uint, req CreateSaleRequest) (*Sale, error) {
	var sale Sale

	err := s.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&vouchers.Voucher{}).
			Clauses(clause.Locking{Strength: "UPDATE"}). // prevent two sales grabbing the same voucher
			Where("status = ? AND user_id = ?", vouchers.VoucherProvisioned, userID)

		if req.VoucherCode != "" {
			q = q.Where("username = ?", req.VoucherCode)
		} else {
			q = q.Where("package_id = ?", req.PackageID)
		}

		if err := q.Preload("Package").Order("created_at asc").First(&sale).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoVoucherAvailable
			}
			return err
		}

		now := time.Now()
		updates := map[string]interface{}{
			"status":     vouchers.VoucherUsed,
			"channel":    strings.TrimSpace(req.Channel),
			"payer":      strings.TrimSpace(req.Payer),
			"payer_name": strings.TrimSpace(req.PayerName),
			"sold_at":    now,
		}

		// Carry the package's validity window onto the sale so expiry can
		// be enforced and reported without re-deriving it later.
		if sale.Package != nil {
			if duration := parseDuration(sale.Package.Duration); duration > 0 {
				expiresAt := now.Add(duration)
				updates["expires_at"] = expiresAt
				updates["expires_on"] = expiresAt.Format("02 Jan 2006 15:04")
			}
		}

		if err := tx.Model(&sale).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Preload("Package").Preload("Agent").First(&sale, sale.ID).Error
	})

	if err != nil {
		return nil, err
	}
	return &sale, nil
}

// updateSale applies a whitelisted set of edits to a sale. The voucher
// code, package, price and sale state are deliberately not editable.
func (s *Service) updateSale(userID, id uint, body UpdateSaleRequest) (*Sale, error) {
	var existing Sale
	if err := s.db.Where("id = ? AND user_id = ?", id, userID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSaleNotFound
		}
		return nil, err
	}

	updates := map[string]interface{}{}
	if body.Channel != nil {
		updates["channel"] = strings.TrimSpace(*body.Channel)
	}
	if body.Payer != nil {
		updates["payer"] = strings.TrimSpace(*body.Payer)
	}
	if body.PayerName != nil {
		updates["payer_name"] = strings.TrimSpace(*body.PayerName)
	}
	if body.AgentID != nil {
		updates["agent_id"] = *body.AgentID
	}
	if body.Note != nil {
		updates["note"] = *body.Note
	}
	if body.UseCase != nil {
		updates["use_case"] = *body.UseCase
	}

	if len(updates) > 0 {
		if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	if err := s.db.Preload("Package").Preload("Agent").First(&existing, existing.ID).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

// deleteSale soft-deletes the underlying voucher row. Use with care: this
// removes it from the voucher module's view too, since both modules share
// the same table.
func (s *Service) deleteSale(userID, id uint) error {
	res := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&vouchers.Voucher{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSaleNotFound
	}
	return nil
}

func (s *Service) bulkDeleteSales(userID uint, ids []uint) error {
	return s.db.Where("id IN ? AND user_id = ?", ids, userID).Delete(&vouchers.Voucher{}).Error
}

// restoreSale brings a soft-deleted sale back from the trash.
func (s *Service) restoreSale(userID, id uint) error {
	res := s.db.Unscoped().Model(&vouchers.Voucher{}).
		Where("id = ? AND user_id = ? AND deleted_at IS NOT NULL", id, userID).
		Updates(map[string]interface{}{"deleted_at": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSaleNotFound
	}
	return nil
}

func (s *Service) bulkRestoreSales(userID uint, ids []uint) error {
	return s.db.Unscoped().Model(&vouchers.Voucher{}).
		Where("id IN ? AND user_id = ? AND deleted_at IS NOT NULL", ids, userID).
		Updates(map[string]interface{}{"deleted_at": nil}).Error
}

// ---------------------------------------------------------------------
// MikroTik provisioning
// ---------------------------------------------------------------------

// autoProvisionEnabled resolves whether a sold voucher should be pushed to
// the router automatically. The request may override the tenant setting.
func (s *Service) autoProvisionEnabled(userID uint, override *bool) bool {
	if override != nil {
		return *override
	}

	var stored settings.Setting
	if err := s.db.Where("key = ? AND user_id = ?", "advanced.autoProvision", userID).First(&stored).Error; err != nil {
		// Default to enabled - the tenant has not opted out.
		return true
	}

	return stored.Value != "false"
}

// provisionOnRouter creates the hotspot user for a sold voucher on the
// given router and records the provisioning on the voucher.
func (s *Service) provisionOnRouter(userID, routerID uint, sale *Sale) error {
	if sale == nil {
		return errors.New("sale is required")
	}

	rs := router.NewService(s.db)

	var rtr router.Router
	if err := s.db.Where("id = ? AND user_id = ?", routerID, userID).First(&rtr).Error; err != nil {
		return errors.New("router not found")
	}
	if !rtr.IsActive {
		return errors.New("router is not active")
	}

	hotspotUser, err := rs.ProvisionHotspotUser(routerID, router.ProvisionHotspotUserRequest{
		Username: sale.Username,
		Comment:  "voucher " + sale.Username,
	}, sale.Package)
	if err != nil {
		return err
	}
	if hotspotUser.Name != sale.Username {
		return errors.New("the router created an unexpected hotspot user")
	}

	now := time.Now()
	if err := s.db.Model(&vouchers.Voucher{}).Where("id = ?", sale.ID).Updates(map[string]interface{}{
		"provisioned":    true,
		"provisioned_at": now,
		"router_id":      routerID,
	}).Error; err != nil {
		return err
	}

	return nil
}

// companyUserIDs returns the user IDs whose sales should be aggregated for
// the company owned by ownerID: the owner itself plus every member of that
// company. Falls back to just ownerID when no company is found, so callers
// never end up with an empty IN-list.
func (s *Service) companyUserIDs(ownerID uint) []uint {
	ids := []uint{ownerID}

	var company shared.Company
	if err := s.db.Preload("Members").Where("user_id = ?", ownerID).First(&company).Error; err != nil {
		return ids
	}

	for _, m := range company.Members {
		ids = append(ids, m.ID)
	}
	return ids
}
// parseDuration converts a package duration such as "24HRS", "7DAYS" or
// "1H 30M" into a time.Duration. It returns 0 when the duration cannot be
// understood.
func parseDuration(duration string) time.Duration {
	s := strings.ToUpper(strings.TrimSpace(duration))
	if s == "" {
		return 0
	}

	// Split into number/unit pairs, e.g. "1H 30M".
	total := time.Duration(0)
	number := ""
	for _, r := range s {
		if r >= '0' && r <= '9' {
			number += string(r)
			continue
		}

		value := 0
		for _, digit := range number {
			value = value*10 + int(digit-'0')
		}
		number = ""

		switch r {
		case 'D':
			total += time.Duration(value) * 24 * time.Hour
		case 'H':
			total += time.Duration(value) * time.Hour
		case 'M':
			total += time.Duration(value) * time.Minute
		case 'S':
			total += time.Duration(value) * time.Second
		}
	}

	return total
}
