package support

import (
	"strconv"
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/packages"
)

// SearchResult is one recent sale, shaped for the support hub's search
// panel.
type SearchResult struct {
	ID            uint   `json:"id"`
	Code          string `json:"code"`
	PaymentMethod string `json:"paymentMethod"`
	CustomerName  string `json:"customerName"`
	Phone         string `json:"phone"`
	PackageLabel  string `json:"packageLabel"`
	PackagePrice  int    `json:"packagePrice"`
	Currency      string `json:"currency"`
	RelativeTime  string `json:"relativeTime"`
	SoldAt        string `json:"soldAt"`
	LoggedIn      bool   `json:"loggedIn"`
	ExpiresOn     string `json:"expiresOn"`
}

// AlertItem is a voucher that was paid for but never connected to the
// hotspot.
type AlertItem struct {
	ID                  uint   `json:"id"`
	Code                string `json:"code"`
	Name                string `json:"name"`
	Phone               string `json:"phone"`
	PackageLabel        string `json:"packageLabel"`
	PaymentMethod       string `json:"paymentMethod"`
	PurchasedLabel      string `json:"purchasedLabel"`
	HoursSincePurchase  int    `json:"hoursSincePurchase"`
	ExpiresOn           string `json:"expiresOn"`
	ProvisionedOnRouter bool   `json:"provisionedOnRouter"`
}

// SupportStats are the headline numbers on the support hub.
type SupportStats struct {
	// AvgLoginTimeMinutes is the mean time between selling a voucher
	// and the customer first logging in.
	AvgLoginTimeMinutes int `json:"avgLoginTimeMinutes"`
	// AvgLoginTimeLabel is the pre-formatted version of the above.
	AvgLoginTimeLabel string `json:"avgLoginTimeLabel"`
	// LoginRate is the percentage of vouchers sold in the last 30 days
	// that have been used at least once.
	LoginRate float64 `json:"loginRate"`
	// NeverLoggedIn counts vouchers sold in the last 7 days that have
	// never been used.
	NeverLoggedIn int64 `json:"neverLoggedIn"`
	// ExpiredToday counts vouchers whose validity ended today.
	ExpiredToday int64 `json:"expiredToday"`
	// TotalSold is the number of vouchers sold in the last 30 days.
	TotalSold int64 `json:"totalSold"`
}

// LoginTimePoint is one point on the "time to first login" chart.
type LoginTimePoint struct {
	Label string  `json:"label"`
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// SupportOverview is the combined payload of the support dashboard.
type SupportOverview struct {
	Stats       SupportStats     `json:"stats"`
	Results     []SearchResult   `json:"results"`
	Alerts      []AlertItem      `json:"alerts"`
	LoginSeries []LoginTimePoint `json:"loginSeries"`
}

// PriceLabel renders a package price the way the support hub shows it
// (e.g. "24HRS @1000").
func PriceLabel(pkgName string, price packages.Price) string {
	if pkgName == "" {
		return ""
	}
	return pkgName
}

// RelativeTime renders how long ago a timestamp was, in the wording the
// support hub uses.
func RelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "about 1 minute ago"
		}
		return "about " + strconv.Itoa(mins) + " minutes ago"
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "about 1 hour ago"
		}
		return "about " + strconv.Itoa(hours) + " hours ago"
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return strconv.Itoa(days) + " days ago"
	}
}

// PurchasedLabel renders how long ago a purchase happened, in the
// wording the support hub's alert list uses.
func PurchasedLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	hours := int(time.Since(t).Hours())
	switch {
	case hours < 24:
		return "Purchased about " + strconv.Itoa(hours) + " hours ago"
	case hours < 48:
		return "Purchased 1 day ago"
	default:
		return "Purchased " + strconv.Itoa(hours/24) + " days ago"
	}
}
