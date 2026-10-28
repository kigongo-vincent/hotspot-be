package dashboard

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/transactions"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"
	"gorm.io/gorm"
)

func NewService(db *gorm.DB) *DashboardService {
	return &DashboardService{DB: db}
}

func (s *DashboardService) Index(c *fiber.Ctx) error {
	var body DashboardRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to get data")
	}

	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	channelFilter := strings.ToLower(strings.TrimSpace(body.Channel))
	voucherEnabled := channelFilter == "" || channelFilter == "all" || channelFilter == "voucher" || channelFilter == "vouchers" || channelFilter == "voucher_gateway"
	mobileEnabled := channelFilter == "" || channelFilter == "all" || channelFilter == "mobile" || channelFilter == "mobile_money" || channelFilter == "mm"

	// --- Sales totals (voucher channel vs mobile money channel) ---
	var voucherSalesCount int64
	var mmSalesCount int64
	if voucherEnabled {
		voucherCountQuery := s.DB.Model(&vouchers.Voucher{}).
			Where("user_id = ? AND status = ? AND channel = ?", userID, vouchers.VoucherUsed, "voucher_gateway")
		if body.Start != nil {
			voucherCountQuery = voucherCountQuery.Where("sold_at >= ?", *body.Start)
		}
		if body.End != nil {
			voucherCountQuery = voucherCountQuery.Where("sold_at <= ?", *body.End)
		}
		voucherCountQuery.Count(&voucherSalesCount)
	}
	if mobileEnabled {
		mmCountQuery := s.DB.Model(&vouchers.Voucher{}).
			Where("user_id = ? AND status = ? AND channel != ? AND channel != ''", userID, vouchers.VoucherUsed, "voucher_gateway")
		if body.Start != nil {
			mmCountQuery = mmCountQuery.Where("sold_at >= ?", *body.Start)
		}
		if body.End != nil {
			mmCountQuery = mmCountQuery.Where("sold_at <= ?", *body.End)
		}
		mmCountQuery.Count(&mmSalesCount)
	}

	// Revenue by channel — load sold vouchers with packages and sum in Go
	// (Price is a Go struct stored as JSON; SQL casting is fragile and
	// dialect-specific, so we sum in application code instead).
	vValue := 0
	mmValue := 0
	if voucherEnabled {
		voucherQuery := s.DB.Preload("Package").
			Where("user_id = ? AND status = ? AND channel = ?", userID, vouchers.VoucherUsed, "voucher_gateway")
		if body.Start != nil {
			voucherQuery = voucherQuery.Where("sold_at >= ?", *body.Start)
		}
		if body.End != nil {
			voucherQuery = voucherQuery.Where("sold_at <= ?", *body.End)
		}
		var voucherSales []vouchers.Voucher
		voucherQuery.Find(&voucherSales)
		for _, v := range voucherSales {
			if v.Package != nil {
				vValue += v.Package.Price.Data().Value
			}
		}
	}
	if mobileEnabled {
		mmQuery := s.DB.Preload("Package").
			Where("user_id = ? AND status = ? AND channel != ? AND channel != ''", userID, vouchers.VoucherUsed, "voucher_gateway")
		if body.Start != nil {
			mmQuery = mmQuery.Where("sold_at >= ?", *body.Start)
		}
		if body.End != nil {
			mmQuery = mmQuery.Where("sold_at <= ?", *body.End)
		}
		var mmSales []vouchers.Voucher
		mmQuery.Find(&mmSales)
		for _, v := range mmSales {
			if v.Package != nil {
				mmValue += v.Package.Price.Data().Value
			}
		}
	}

	// --- Balance: last transaction balance for this user ---
	var lastTx transactions.Transaction
	balance := 0.0
	if err := s.DB.Where("user_id = ?", userID).Order("id desc").First(&lastTx).Error; err == nil {
		balance = lastTx.Balance
	}

	// --- Recent sales (last 20 USED vouchers) ---
	recentQuery := s.DB.Preload("Package").
		Where("user_id = ? AND status = ?", userID, vouchers.VoucherUsed)
	if voucherEnabled && !mobileEnabled {
		recentQuery = recentQuery.Where("channel = ?", "voucher_gateway")
	}
	if mobileEnabled && !voucherEnabled {
		recentQuery = recentQuery.Where("channel != ? AND channel != ''", "voucher_gateway")
	}
	if body.Start != nil {
		recentQuery = recentQuery.Where("sold_at >= ?", *body.Start)
	}
	if body.End != nil {
		recentQuery = recentQuery.Where("sold_at <= ?", *body.End)
	}
	var recentVouchers []vouchers.Voucher
	recentQuery.
		Order("sold_at desc nulls last, id desc").
		Limit(20).
		Find(&recentVouchers)

	// --- Overview data: proceeds/commission/gross by day ---
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := startOfToday.AddDate(0, 0, -6)
	windowEnd := startOfToday.AddDate(0, 0, 1)
	if body.Start != nil {
		windowStart = body.Start.UTC().Truncate(24 * time.Hour)
	}
	if body.End != nil {
		// include the full end day
		windowEnd = body.End.UTC().Add(24*time.Hour - time.Second)
	}
	if windowEnd.Before(windowStart) {
		windowStart, windowEnd = windowEnd, windowStart
	}
	// cap the range to 31 days to keep the chart readable
	if windowEnd.Sub(windowStart) > 31*24*time.Hour {
		windowStart = windowEnd.AddDate(0, 0, -31)
	}

	overviewQuery := s.DB.Preload("Package").
		Where("user_id = ? AND status = ? AND sold_at >= ?", userID, vouchers.VoucherUsed, windowStart)
	if voucherEnabled && !mobileEnabled {
		overviewQuery = overviewQuery.Where("channel = ?", "voucher_gateway")
	}
	if mobileEnabled && !voucherEnabled {
		overviewQuery = overviewQuery.Where("channel != ? AND channel != ''", "voucher_gateway")
	}
	if body.End != nil {
		overviewQuery = overviewQuery.Where("sold_at <= ?", *body.End)
	} else {
		overviewQuery = overviewQuery.Where("sold_at <= ?", windowEnd)
	}
	var soldVouchers []vouchers.Voucher
	overviewQuery.Find(&soldVouchers)

	type dayTotals struct {
		gross      int
		commission int
	}
	byDay := make(map[string]dayTotals, 7)
	for _, v := range soldVouchers {
		if v.SoldAt == nil {
			continue
		}
		key := v.SoldAt.Format("2006-01-02")
		cur := byDay[key]
		if v.Package != nil {
			price := v.Package.Price.Data().Value
			cur.gross += price
			// commission placeholder: 10% of gross
			cur.commission += price / 10
		}
		byDay[key] = cur
	}

	days := int(windowEnd.Sub(windowStart).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	if days > 31 {
		days = 31
	}

	overviewData := make([]OverviewDataPoint, 0, days)
	for i := 0; i < days; i++ {
		day := windowStart.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		t := byDay[key]
		overviewData = append(overviewData, OverviewDataPoint{
			Date:       day.Format("Jan 2"),
			Gross:      t.gross,
			Commission: t.commission,
			Proceeds:   t.gross - t.commission,
		})
	}

	return c.JSON(base.APIResponse{
		Data: DashboardResponse{
			Sales: Sales{
				MM: packages.Price{Currency: packages.UGX, Value: mmValue},
				V:  packages.Price{Currency: packages.UGX, Value: vValue},
			},
			Balance: packages.Price{Currency: packages.UGX, Value: int(balance)},
			SystemInsights: SystemInsights{
				Active:    "0",
				CPU:       "0%",
				DataUsage: "0KB",
			},
			SystemInsight: SystemInsights{
				Active:    "0",
				CPU:       "0%",
				DataUsage: "0KB",
			},
			RecentSales:  recentVouchers,
			OverviewData: overviewData,
		},
	})
}

func (s *DashboardService) OverviewData(c *fiber.Ctx) error {
	return c.JSON(base.APIResponse{Data: OverviewDataResponse{OverviewData: []OverviewDataPoint{}}})
}

func (s *DashboardService) UsageAnalytics(c *fiber.Ctx) error {
	return c.JSON(base.APIResponse{})
}
