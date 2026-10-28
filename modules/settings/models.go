package settings

import (
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/gorm"
)

// Setting is a single tenant-scoped configuration value. Settings are
// stored as key/value pairs so new configuration can be introduced
// without a schema change, while the key catalogue below keeps the
// values the UI binds to discoverable and documented.
type Setting struct {
	gorm.Model
	Key   string `json:"key" gorm:"uniqueIndex:idx_setting_owner;not null"`
	Value string `json:"value"`
	// UserID scopes the setting to a tenant.
	UserID *uint       `json:"userId" gorm:"uniqueIndex:idx_setting_owner"`
	User   *shared.User `json:"user" gorm:"foreignKey:UserID"`
}

// SettingDefinition documents one configuration key: what it is for and
// the value used when the tenant has never set it.
type SettingDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

// KnownSettings is the catalogue of configuration keys the product
// understands. GET /settings/catalogue returns it so the UI never has
// to hardcode key names.
var KnownSettings = []SettingDefinition{
	{Key: "general.businessName", Label: "Business name", Group: "general", Type: "string", Default: "", Description: "Name shown on the captive portal and invoices."},
	{Key: "general.timezone", Label: "Timezone", Group: "general", Type: "string", Default: "Africa/Kampala", Description: "Timezone used for daily reports and expiry times."},
	{Key: "general.currency", Label: "Currency", Group: "general", Type: "string", Default: "UGX", Description: "Default currency for packages and reports."},
	{Key: "general.supportEmail", Label: "Support email", Group: "general", Type: "string", Default: "", Description: "Address shown to customers who need help."},
	{Key: "general.supportPhone", Label: "Support phone", Group: "general", Type: "string", Default: "", Description: "Phone number shown to customers who need help."},

	{Key: "routers.autoApprove", Label: "Auto-approve new routers", Group: "routers", Type: "boolean", Default: "false", Description: "Skip manual approval when a router first connects."},
	{Key: "routers.defaultRadiusMode", Label: "Default RADIUS mode", Group: "routers", Type: "boolean", Default: "true", Description: "Applied to routers without an explicit override."},
	{Key: "routers.defaultLanCidr", Label: "Default LAN subnet", Group: "routers", Type: "string", Default: "", Description: "Subnet assigned to newly provisioned routers. Leave blank for automatic allocation."},

	{Key: "sms.agentSales", Label: "Agent sale notifications", Group: "sms", Type: "boolean", Default: "false", Description: "Send an SMS when an agent registers a voucher for a customer."},
	{Key: "sms.realtimePurchases", Label: "Realtime purchase notifications", Group: "sms", Type: "boolean", Default: "false", Description: "Send the buyer their code as soon as the payment clears."},
	{Key: "sms.loginReminderMinutes", Label: "Delayed login reminder", Group: "sms", Type: "number", Default: "0", Description: "Minutes after purchase to remind a buyer who has not logged in. 0 disables the reminder."},
	{Key: "sms.ratePerMessage", Label: "Rate per SMS", Group: "sms", Type: "number", Default: "35", Description: "Cost charged to the account for each SMS sent."},

	{Key: "payments.mobileMoney", Label: "Mobile Money", Group: "payments", Type: "boolean", Default: "true", Description: "Accept mobile money payments."},
	{Key: "payments.airtelMoney", Label: "Airtel Money", Group: "payments", Type: "boolean", Default: "false", Description: "Accept Airtel Money payments."},

	{Key: "advanced.autoProvision", Label: "Auto-provision sold vouchers", Group: "advanced", Type: "boolean", Default: "true", Description: "Push a sold voucher onto the customer's router as a hotspot user automatically."},
	{Key: "advanced.loginReminderEnabled", Label: "Login reminders", Group: "advanced", Type: "boolean", Default: "true", Description: "Track vouchers that were paid for but never used."},
}

// DefaultValue returns the catalogue default for a key.
func DefaultValue(key string) string {
	for _, s := range KnownSettings {
		if s.Key == key {
			return s.Default
		}
	}
	return ""
}

// SettingResponse is the payload of GET /settings.
type SettingResponse struct {
	// Settings is the resolved key/value map - every catalogue key is
	// present, falling back to its default.
	Settings map[string]string `json:"settings"`
	// Defined lists the keys the product understands.
	Defined []SettingDefinition `json:"defined"`
}

// SettingUpsertRequest is the payload of PUT /settings.
type SettingUpsertRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// BulkUpsertRequest is the payload of POST /settings/bulk.
type BulkUpsertRequest struct {
	Settings map[string]string `json:"settings"`
}
