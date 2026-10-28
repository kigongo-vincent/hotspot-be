package support

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"
	"gorm.io/gorm"
)

// SupportService powers the Support Intelligence Hub. Every figure it
// returns is derived from the sale and usage history recorded on
// vouchers - there is no separate analytics table to drift out of sync.
type SupportService struct {
	DB *gorm.DB
}

// NewService builds the support service.
func NewService(db *gorm.DB) *SupportService {
	return &SupportService{DB: db}
}

// packageLabel renders a package as "24HRS @1000", the format used
// across the support hub.
func packageLabel(pkg *packages.Package) string {
	if pkg == nil {
		return ""
	}

	price := pkg.Price.Data()
	name := pkg.Name
	if price.Value == 0 {
		return name
	}
	return fmt.Sprintf("%s @%d", name, price.Value)
}

// paymentLabel renders a sale channel the way the support hub shows it.
func paymentLabel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "mobile-money", "mobilemoney", "momo":
		return "Mobile Money"
	case "agent":
		return "Agent"
	case "manual":
		return "Manual"
	case "card":
		return "Card"
	case "":
		return "Unpaid"
	default:
		return channel
	}
}

// soldVouchers loads the vouchers sold by this tenant, newest first.
func (s *SupportService) soldVouchers(userID uint, search string, limit int) ([]vouchers.Voucher, error) {
	query := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL", userID).
		Preload("Package")

	if q := strings.TrimSpace(search); q != "" {
		like := "%" + q + "%"
		query = query.Where(
			"username ILIKE ? OR payer ILIKE ? OR payer_name ILIKE ? OR note ILIKE ?",
			like, like, like, like,
		)
	}

	var rows []vouchers.Voucher
	if err := query.Order("sold_at desc, id desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// toSearchResult shapes a sold voucher for the search panel.
func toSearchResult(v vouchers.Voucher) SearchResult {
	soldAt := time.Time{}
	if v.SoldAt != nil {
		soldAt = *v.SoldAt
	}

	return SearchResult{
		ID:            v.ID,
		Code:          v.Username,
		PaymentMethod: paymentLabel(v.Channel),
		CustomerName:  v.PayerName,
		Phone:         v.Payer,
		PackageLabel:  packageLabel(v.Package),
		PackagePrice:  v.Package.Price.Data().Value,
		Currency:      string(v.Package.Price.Data().Currency),
		RelativeTime:  RelativeTime(soldAt),
		SoldAt:        soldAt.Format(time.RFC3339),
		LoggedIn:      v.FirstLoginAt != nil,
		ExpiresOn:     v.ExpiresOn,
	}
}

// toAlert shapes a paid-but-never-connected voucher.
func toAlert(v vouchers.Voucher) AlertItem {
	soldAt := time.Time{}
	if v.SoldAt != nil {
		soldAt = *v.SoldAt
	}

	hours := 0
	if !soldAt.IsZero() {
		hours = int(time.Since(soldAt).Hours())
	}

	return AlertItem{
		ID:                  v.ID,
		Code:                v.Username,
		Name:                v.PayerName,
		Phone:               v.Payer,
		PackageLabel:        packageLabel(v.Package),
		PaymentMethod:       paymentLabel(v.Channel),
		PurchasedLabel:      PurchasedLabel(soldAt),
		HoursSincePurchase:  hours,
		ExpiresOn:           v.ExpiresOn,
		ProvisionedOnRouter: v.Provisioned,
	}
}

// Search handles GET /support/search?q=...
func (s *SupportService) Search(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	rows, err := s.soldVouchers(userID, c.Query("q"), 25)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to search sales")
	}

	results := make([]SearchResult, 0, len(rows))
	for _, v := range rows {
		results = append(results, toSearchResult(v))
	}

	return base.OK(c, map[string]interface{}{"results": results}, "search complete")
}

// Alerts handles GET /support/alerts. It returns vouchers sold in the
// last 7 days that have never been used - the customers most likely to
// need support.
func (s *SupportService) Alerts(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	cutoff := time.Now().AddDate(0, 0, -7)

	var rows []vouchers.Voucher
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL AND sold_at >= ? AND first_login_at IS NULL", userID, cutoff).
		Preload("Package").
		Order("sold_at desc").
		Find(&rows).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to load alerts")
	}

	alerts := make([]AlertItem, 0, len(rows))
	for _, v := range rows {
		alerts = append(alerts, toAlert(v))
	}

	return base.OK(c, map[string]interface{}{"alerts": alerts}, "alerts loaded")
}

// Stats handles GET /support/stats.
func (s *SupportService) Stats(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	stats, err := s.computeStats(userID)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to compute stats")
	}

	return base.OK(c, stats, "stats computed")
}

// computeStats derives the headline support numbers.
func (s *SupportService) computeStats(userID uint) (SupportStats, error) {
	stats := SupportStats{}

	now := time.Now()
	thirtyDays := now.AddDate(0, 0, -30)
	sevenDays := now.AddDate(0, 0, -7)
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfToday := startOfToday.AddDate(0, 0, 1)

	// Vouchers sold in the last 30 days.
	var sold []vouchers.Voucher
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL AND sold_at >= ?", userID, thirtyDays).
		Find(&sold).Error; err != nil {
		return stats, err
	}

	stats.TotalSold = int64(len(sold))

	used := 0
	loginMinutes := 0.0
	loginSamples := 0
	for _, v := range sold {
		if v.FirstLoginAt != nil {
			used++
			if v.SoldAt != nil {
				delta := v.FirstLoginAt.Sub(*v.SoldAt).Minutes()
				if delta < 0 {
					delta = 0
				}
				loginMinutes += delta
				loginSamples++
			}
		}
	}

	if loginSamples > 0 {
		stats.AvgLoginTimeMinutes = int(math.Round(loginMinutes / float64(loginSamples)))
	}
	stats.AvgLoginTimeLabel = formatDuration(stats.AvgLoginTimeMinutes)

	if stats.TotalSold > 0 {
		stats.LoginRate = math.Round((float64(used)/float64(stats.TotalSold))*10000) / 100
	}

	// Never logged in - sold in the last 7 days but never used.
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL AND sold_at >= ? AND sold_at <= ? AND first_login_at IS NULL", userID, sevenDays, now).
		Count(&stats.NeverLoggedIn).Error; err != nil {
		return stats, err
	}

	// Expired today.
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND expires_at IS NOT NULL AND expires_at >= ? AND expires_at < ?", userID, startOfToday, endOfToday).
		Count(&stats.ExpiredToday).Error; err != nil {
		return stats, err
	}

	return stats, nil
}

// LoginSeries handles GET /support/login-series. It returns the average
// number of minutes between sale and first login for each of the last
// seven days.
func (s *SupportService) LoginSeries(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	series, err := s.computeLoginSeries(userID, 7)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to compute login series")
	}

	return base.OK(c, map[string]interface{}{"series": series}, "login series computed")
}

// computeLoginSeries averages time-to-first-login per day.
func (s *SupportService) computeLoginSeries(userID uint, days int) ([]LoginTimePoint, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))

	var rows []vouchers.Voucher
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL AND sold_at >= ? AND first_login_at IS NOT NULL", userID, start).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	totals := make(map[string]float64)
	counts := make(map[string]int)

	for _, v := range rows {
		if v.SoldAt == nil || v.FirstLoginAt == nil {
			continue
		}
		key := v.SoldAt.Format("2006-01-02")
		delta := v.FirstLoginAt.Sub(*v.SoldAt).Minutes()
		if delta < 0 {
			delta = 0
		}
		totals[key] += delta
		counts[key]++
	}

	series := make([]LoginTimePoint, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		key := day.Format("2006-01-02")

		value := 0.0
		if counts[key] > 0 {
			value = math.Round((totals[key]/float64(counts[key]))*10) / 10
		}

		series = append(series, LoginTimePoint{
			Label: day.Format("Mon 02"),
			Date:  key,
			Value: value,
		})
	}

	return series, nil
}

// Overview handles GET /support/overview - everything the hub needs in
// a single round trip.
func (s *SupportService) Overview(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	stats, err := s.computeStats(userID)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to compute stats")
	}

	rows, err := s.soldVouchers(userID, c.Query("q"), 25)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to load sales")
	}
	results := make([]SearchResult, 0, len(rows))
	for _, v := range rows {
		results = append(results, toSearchResult(v))
	}

	cutoff := time.Now().AddDate(0, 0, -7)
	var neverUsed []vouchers.Voucher
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("user_id = ? AND sold_at IS NOT NULL AND sold_at >= ? AND first_login_at IS NULL", userID, cutoff).
		Preload("Package").
		Order("sold_at desc").
		Find(&neverUsed).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to load alerts")
	}
	alerts := make([]AlertItem, 0, len(neverUsed))
	for _, v := range neverUsed {
		alerts = append(alerts, toAlert(v))
	}

	series, err := s.computeLoginSeries(userID, 7)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to compute login series")
	}

	return base.OK(c, SupportOverview{
		Stats:       stats,
		Results:     results,
		Alerts:      alerts,
		LoginSeries: series,
	}, "overview loaded")
}

// formatDuration renders a number of minutes as the compact label the
// support hub shows (e.g. "40m", "2h 15m").
func formatDuration(minutes int) string {
	if minutes <= 0 {
		return "0m"
	}

	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}

	hours := minutes / 60
	remainder := minutes % 60
	if remainder == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, remainder)
}
