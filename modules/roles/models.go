package roles

import (
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// PermissionAction is a single capability that can be granted on a
// resource.
type PermissionAction string

const (
	ActionRead   PermissionAction = "read"
	ActionWrite  PermissionAction = "write"
	ActionDelete PermissionAction = "delete"
)

// IsValid reports whether the action is one of the three the UI offers.
func (a PermissionAction) IsValid() bool {
	switch a {
	case ActionRead, ActionWrite, ActionDelete:
		return true
	default:
		return false
	}
}

// ResourcePermission grants a set of actions on one resource.
type ResourcePermission struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

// Role is a named bundle of permissions that can be assigned to the
// members of a company.
type Role struct {
	gorm.Model
	Name        string `json:"name" gorm:"not null"`
	Description string `json:"description"`
	// Permissions is the list of resource/action grants.
	Permissions datatypes.JSONType[[]ResourcePermission] `json:"permissions"`
	// IsSystem marks a role that ships with the product and cannot be
	// deleted (only edited).
	IsSystem bool `json:"isSystem" gorm:"default:false"`
	// UserID is the owner of the role - roles are scoped to the
	// company that created them.
	UserID *uint       `json:"userId"`
	User   *shared.User `json:"user" gorm:"foreignKey:UserID"`
}

// RoleRequest is the payload for creating or updating a role.
type RoleRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Permissions []ResourcePermission `json:"permissions"`
}

// RoleResponse is the flat shape returned to the client.
type RoleResponse struct {
	ID          uint                 `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Permissions []ResourcePermission `json:"permissions"`
	IsSystem    bool                 `json:"isSystem"`
	CreatedAt   string               `json:"createdAt"`
}

// RoleListResponse is the payload of GET /roles/list.
type RoleListResponse struct {
	Roles []RoleResponse `json:"roles"`
}

// PermissionCatalogue describes the resources and actions a role can be
// granted on. It is returned by GET /roles/catalogue so the UI never
// hardcodes the list.
type PermissionCatalogue struct {
	Resources []string           `json:"resources"`
	Actions   []PermissionAction `json:"actions"`
}

// KnownResources is the catalogue of resources a role may be granted
// permissions on.
var KnownResources = []string{
	"Users",
	"Packages",
	"Routers",
	"Transactions",
	"Disbursements",
	"Vouchers",
	"Sales",
	"Roles",
}

// KnownActions is the catalogue of grantable actions.
var KnownActions = []PermissionAction{ActionRead, ActionWrite, ActionDelete}

// DefaultRoles are seeded for a new tenant the first time the roles
// endpoint is called.
func DefaultRoles() []Role {
	return []Role{
		{
			Name:        "Hotspot Admin",
			Description: "Full administrative access to every resource.",
			IsSystem:    true,
			Permissions: datatypes.NewJSONType(fullPermissions()),
		},
		{
			Name:        "Agent",
			Description: "Read-only access to vouchers and sales.",
			IsSystem:    true,
			Permissions: datatypes.NewJSONType([]ResourcePermission{
				{Resource: "Vouchers", Actions: []string{string(ActionRead)}},
				{Resource: "Sales", Actions: []string{string(ActionRead)}},
			}),
		},
	}
}

func fullPermissions() []ResourcePermission {
	perms := make([]ResourcePermission, 0, len(KnownResources))
	for _, resource := range KnownResources {
		perms = append(perms, ResourcePermission{
			Resource: resource,
			Actions:  []string{string(ActionRead), string(ActionWrite), string(ActionDelete)},
		})
	}
	return perms
}

// SanitizePermissions drops unknown resources and actions so a crafted
// payload cannot grant a capability the product does not define.
func SanitizePermissions(in []ResourcePermission) []ResourcePermission {
	knownResources := map[string]bool{}
	for _, r := range KnownResources {
		knownResources[r] = true
	}

	out := make([]ResourcePermission, 0, len(in))
	for _, p := range in {
		if !knownResources[p.Resource] {
			continue
		}

		actions := make([]string, 0, len(p.Actions))
		for _, a := range p.Actions {
			if PermissionAction(a).IsValid() {
				actions = append(actions, a)
			}
		}

		out = append(out, ResourcePermission{Resource: p.Resource, Actions: actions})
	}
	return out
}

// CreatedAtString formats a role's creation timestamp for display.
func CreatedAtString(t time.Time) string {
	return t.Format("02 Jan 2006 15:04")
}
