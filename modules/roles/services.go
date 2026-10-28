package roles

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// RoleService owns the role catalogue for a tenant.
type RoleService struct {
	DB *gorm.DB
}

// NewService builds the role service.
func NewService(db *gorm.DB) *RoleService {
	return &RoleService{DB: db}
}

// toResponse flattens a role for the client.
func toResponse(r Role) RoleResponse {
	permissions := r.Permissions.Data()
	if permissions == nil {
		permissions = []ResourcePermission{}
	}

	return RoleResponse{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Permissions: permissions,
		IsSystem:    r.IsSystem,
		CreatedAt:   CreatedAtString(r.CreatedAt),
	}
}

// seed ensures the built-in roles exist for a tenant. Seeding is
// idempotent: a role that already exists (by name) is left untouched.
func (s *RoleService) seed(userID uint) error {
	for _, role := range DefaultRoles() {
		var count int64
		if err := s.DB.Model(&Role{}).
			Where("user_id = ? AND name = ?", userID, role.Name).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		role.UserID = &userID
		if err := s.DB.Create(&role).Error; err != nil {
			return err
		}
	}
	return nil
}

// List handles GET /roles/list.
func (s *RoleService) List(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	if err := s.seed(userID); err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to load roles")
	}

	var roles []Role
	if err := s.DB.Where("user_id = ?", userID).Order("is_system desc, id asc").Find(&roles).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to fetch roles")
	}

	resp := make([]RoleResponse, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, toResponse(r))
	}

	return base.OK(c, RoleListResponse{Roles: resp}, "roles fetched")
}

// Catalogue handles GET /roles/catalogue.
func (s *RoleService) Catalogue(c *fiber.Ctx) error {
	return base.OK(c, PermissionCatalogue{
		Resources: KnownResources,
		Actions:   KnownActions,
	}, "catalogue fetched")
}

// FindByID handles GET /roles/:id.
func (s *RoleService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var role Role
	if err := s.DB.Where("id = ? AND user_id = ?", c.Params("id"), userID).First(&role).Error; err != nil {
		return base.APIError(c, fiber.StatusNotFound, "role not found")
	}

	return base.OK(c, toResponse(role), "role fetched")
}

// Create handles POST /roles.
func (s *RoleService) Create(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var body RoleRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		return base.APIError(c, fiber.StatusBadRequest, "role name is required")
	}

	var count int64
	if err := s.DB.Model(&Role{}).Where("user_id = ? AND name = ?", userID, name).Count(&count).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to check for duplicates")
	}
	if count > 0 {
		return base.APIError(c, fiber.StatusConflict, "a role with that name already exists")
	}

	role := Role{
		Name:        name,
		Description: strings.TrimSpace(body.Description),
		Permissions: datatypes.NewJSONType(SanitizePermissions(body.Permissions)),
		IsSystem:    false,
		UserID:      &userID,
	}

	if err := s.DB.Create(&role).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to create role")
	}

	return base.OK(c, toResponse(role), "role created")
}

// Update handles PUT /roles/:id.
func (s *RoleService) Update(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var role Role
	if err := s.DB.Where("id = ? AND user_id = ?", c.Params("id"), userID).First(&role).Error; err != nil {
		return base.APIError(c, fiber.StatusNotFound, "role not found")
	}

	var body RoleRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	updates := map[string]interface{}{}
	if name := strings.TrimSpace(body.Name); name != "" && name != role.Name {
		updates["name"] = name
	}
	if body.Description != "" {
		updates["description"] = strings.TrimSpace(body.Description)
	}
	if body.Permissions != nil {
		updates["permissions"] = datatypes.NewJSONType(SanitizePermissions(body.Permissions))
	}

	if len(updates) > 0 {
		if err := s.DB.Model(&role).Updates(updates).Error; err != nil {
			return base.APIError(c, fiber.StatusInternalServerError, "failed to update role")
		}
	}

	if err := s.DB.First(&role, role.ID).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to reload role")
	}

	return base.OK(c, toResponse(role), "role updated")
}

// Delete handles DELETE /roles/:id. Built-in roles cannot be deleted.
func (s *RoleService) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var role Role
	if err := s.DB.Where("id = ? AND user_id = ?", c.Params("id"), userID).First(&role).Error; err != nil {
		return base.APIError(c, fiber.StatusNotFound, "role not found")
	}

	if role.IsSystem {
		return base.APIError(c, fiber.StatusForbidden, "built-in roles cannot be deleted")
	}

	if err := s.DB.Delete(&role).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to delete role")
	}

	return base.OK(c, nil, "role deleted")
}

// DeleteMany handles POST /roles/bulk-delete.
func (s *RoleService) DeleteMany(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var body struct {
		IDs []uint `json:"ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if len(body.IDs) == 0 {
		return base.APIError(c, fiber.StatusBadRequest, "no ids provided")
	}

	// Built-in roles are excluded from the delete by the is_system = false
	// predicate, so a mixed selection only removes the custom ones.
	if err := s.DB.Where("id IN ? AND user_id = ? AND is_system = ?", body.IDs, userID, false).Delete(&Role{}).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to delete roles")
	}

	return base.OK(c, nil, "roles deleted")
}

// ErrRoleInUse is returned when a role cannot be removed because it is
// still assigned to a member.
var ErrRoleInUse = errors.New("role is still assigned to a member")
