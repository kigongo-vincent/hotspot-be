package dashboard

import (
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/sales"
	"gorm.io/gorm"
)

type DashboardRequest struct {
	Start   *time.Time `json:"start,omitempty"`
	End     *time.Time `json:"end,omitempty"`
	Channel string     `json:"channel,omitempty"`
}

type Sales struct {
	MM packages.Price `json:"mm"`
	V  packages.Price `json:"v"`
}

type SystemInsights struct {
	Active    string `json:"active"`
	CPU       string `json:"cpu"`
	DataUsage string `json:"dataUsage"`
}

type OverviewDataPoint struct {
	Date       string `json:"date"`
	Commission int    `json:"commission"`
	Gross      int    `json:"gross"`
	Proceeds   int    `json:"proceeds"`
}

type OverviewDataResponse struct {
	OverviewData []OverviewDataPoint `json:"overviewData"`
}

type DashboardResponse struct {
	Sales          Sales               `json:"sales"`
	Balance        packages.Price      `json:"balance"`
	SystemInsights SystemInsights      `json:"systemInsights"`
	SystemInsight  SystemInsights      `json:"systemInsight"`
	RecentSales    []sales.Sale        `json:"recentSales"`
	OverviewData   []OverviewDataPoint `json:"overviewData"`
}

type DashboardService struct {
	DB *gorm.DB
}

type UsageAnalyticsRequest struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type OverviewItem[T any] struct {
	Title   string `json:"title"`
	Value   T      `json:"value"`
	Caption string `json:"caption"`
}

type DataUsageAnalyticsDataPoint struct {
}
type DataUsagePerPackageAnalyticsDataPoint struct {
}

type SessionAnalyticsDataPoint struct {
}

type DataUser struct {
}

type UsageAnalyticsResponse[T any] struct {
	Overview            []OverviewItem[T]                       `json:"overview"`
	DataUsageAnalytics  []DataUsageAnalyticsDataPoint           `json:"analytics"`
	SessionAnalytics    []SessionAnalyticsDataPoint             `json:"session"`
	Users               []DataUser                              `json:"user"`
	DataUsagePerPackage []DataUsagePerPackageAnalyticsDataPoint `json:"package"`
}
