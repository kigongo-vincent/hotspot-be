package router

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/vouchers"
	"gorm.io/gorm"
)

// -----------------------------------------------------------------------
// Router CRUD + operator actions
// -----------------------------------------------------------------------

// ownedRouter loads a router by ID, scoped to the authenticated user, so
// one tenant can never read or modify another tenant's routers.
func (s *RouterService) ownedRouter(userID, routerID uint) (Router, error) {
	var rtr Router
	if err := s.DB.Where("id = ? AND user_id = ?", routerID, userID).First(&rtr).Error; err != nil {
		return Router{}, err
	}
	return rtr, nil
}

// routerParam parses the :id route param.
func routerParam(c *fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return 0, errors.New("invalid router id")
	}
	return uint(id), nil
}

// List handles GET /router/list. Returns every router owned by the
// caller with its live connection state and latest telemetry sample.
func (s *RouterService) List(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var routers []Router
	if err := s.DB.Where("user_id = ?", userID).Order("id desc").Find(&routers).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to fetch routers")
	}

	items := make([]RouterListItem, 0, len(routers))
	for _, rtr := range routers {
		items = append(items, s.summariseRouter(rtr))
	}

	return base.OK(c, RouterListResponse{Routers: items}, "routers fetched")
}

// summariseRouter builds the list row for one router: connection state
// plus the most recent telemetry sample.
func (s *RouterService) summariseRouter(rtr Router) RouterListItem {
	item := RouterListItem{
		ID:           rtr.ID,
		Name:         rtr.Name,
		Brand:        rtr.Brand,
		Model:        rtr.DeviceModel,
		Location:     rtr.Location,
		Notes:        rtr.Notes,
		IsActive:     rtr.IsActive,
		RemoteAccess: rtr.RemoteAccess,
		CreatedAt:    rtr.CreatedAt.Format("02 Jan 2006 15:04"),
		VPNStatus:    string(Default),
	}

	var vpn VPN
	if err := s.DB.Where("router_id = ?", rtr.ID).First(&vpn).Error; err == nil {
		item.Connected = vpn.Status == Connected
		item.VPNStatus = string(vpn.Status)
	}

	var stat RouterStat
	if err := s.DB.Where("router_id = ?", rtr.ID).Order("created_at desc").First(&stat).Error; err == nil {
		item.CPU = stat.CPU
		item.Memory = stat.Memory
		item.ActiveUsers = stat.ActiveUsers
		lastSeen := stat.CreatedAt.Format("02 Jan 2006 15:04")
		item.LastSeen = lastSeen
		item.LastStatAt = &lastSeen
	}

	var provisioned int64
	s.DB.Model(&vouchers.Voucher{}).Where("router_id = ? AND provisioned = ?", rtr.ID, true).Count(&provisioned)
	item.Provisioned = provisioned

	return item
}

// FindByID handles GET /router/:id.
func (s *RouterService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}

	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	return base.OK(c, s.summariseRouter(rtr), "router fetched")
}

// Update handles PUT /router/:id. Only the fields present in the
// request body are applied, so a partial update never wipes the rest.
func (s *RouterService) Update(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}

	var body UpdateRouterRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	updates := map[string]interface{}{}
	if body.Name != nil {
		if strings.TrimSpace(*body.Name) == "" {
			return base.APIError(c, fiber.StatusBadRequest, "name cannot be empty")
		}
		updates["name"] = strings.TrimSpace(*body.Name)
	}
	if body.Brand != nil {
		updates["brand"] = strings.TrimSpace(*body.Brand)
	}
	if body.Model != nil {
		updates["model"] = strings.TrimSpace(*body.Model)
	}
	if body.Location != nil {
		updates["location"] = strings.TrimSpace(*body.Location)
	}
	if body.Notes != nil {
		updates["notes"] = *body.Notes
	}
	if body.IsActive != nil {
		updates["is_active"] = *body.IsActive
	}
	if body.RemoteAccess != nil {
		updates["remote_access"] = *body.RemoteAccess
	}

	if len(updates) > 0 {
		if err := s.DB.Model(&rtr).Updates(updates).Error; err != nil {
			return base.APIError(c, fiber.StatusInternalServerError, "failed to update router")
		}
	}

	if err := s.DB.First(&rtr, rtr.ID).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to reload router")
	}

	return base.OK(c, s.summariseRouter(rtr), "router updated")
}

// Delete handles DELETE /router/:id. The router and its VPN record are
// soft-deleted so the telemetry history is not lost.
func (s *RouterService) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}

	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("router_id = ?", rtr.ID).Delete(&VPN{}).Error; err != nil {
			return err
		}
		return tx.Delete(&rtr).Error
	})
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to delete router")
	}

	return base.OK(c, nil, "router deleted")
}

// -----------------------------------------------------------------------
// Credential rotation
// -----------------------------------------------------------------------

// CredentialRotationResult is the payload of the rotation endpoints.
type CredentialRotationResult struct {
	Rotated bool   `json:"rotated"`
	Applied bool   `json:"applied"`
	Script  string `json:"script,omitempty"`
	// Password is returned exactly once, and only when the router could
	// not be reached and the rotation must be applied by hand.
	Password string `json:"password,omitempty"`
	Message  string `json:"message"`
}

// RotateCredentials handles POST /router/:id/rotate-credentials.
//
// When the router is reachable the new credential is applied over the
// API, verified, and persisted in one step - the operator never sees
// the password. When the router is offline the endpoint returns the
// RouterOS script plus the one-time password so the operator can apply
// it by hand and then call the confirm endpoint.
func (s *RouterService) RotateCredentials(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	if _, err := s.ownedRouter(userID, id); err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	// The credential is generated exactly once here. Both the RouterOS
	// script and the encrypted copy persisted afterwards are derived from
	// this same password - generating it twice would leave the backend
	// holding a different password than the one applied on the router.
	username, plaintext, encPassword, err := s.rotateCredential(id)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}
	script := buildRotateAPIUserScript(username, plaintext)

	var vpn VPN
	if err := s.DB.Where("router_id = ?", id).First(&vpn).Error; err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router is not provisioned")
	}

	if vpn.Status == Connected {
		if err := s.applyAPIUserPassword(id, username, plaintext); err != nil {
			return base.APIError(c, fiber.StatusBadGateway, "failed to apply the new credential on the router: "+err.Error())
		}

		// Verify the new credential before persisting it, so a rejected
		// password can never leave the backend unable to talk to the
		// router.
		if _, err := RunCommand(vpn.Address, username, plaintext, "/system/resource/print"); err != nil {
			return base.APIError(c, fiber.StatusBadGateway, "the router rejected the new credential: "+err.Error())
		}

		if err := s.ConfirmCredentialRotation(id, encPassword); err != nil {
			return base.APIError(c, fiber.StatusInternalServerError, err.Error())
		}

		return base.OK(c, CredentialRotationResult{
			Rotated: true,
			Applied: true,
			Message: "The router API password was rotated and verified.",
		}, "credentials rotated")
	}

	// Offline: hand the script and one-time password to the operator.
	return base.OK(c, CredentialRotationResult{
		Rotated:  true,
		Applied:  false,
		Script:   script,
		Password: plaintext,
		Message:  "The router is offline. Run the command on the router, then confirm the rotation.",
	}, "credentials rotated")
}

// ConfirmRotation handles POST /router/:id/rotate-credentials/confirm.
// The operator submits the password they applied on the router; the
// server re-encrypts and stores it.
func (s *RouterService) ConfirmRotation(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	if _, err := s.ownedRouter(userID, id); err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if body.Password == "" {
		return base.APIError(c, fiber.StatusBadRequest, "password is required")
	}

	encPassword, err := EncryptSecret(body.Password)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	if err := s.ConfirmCredentialRotation(id, encPassword); err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, CredentialRotationResult{Rotated: true, Applied: true, Message: "The rotated credential is now active."}, "credentials confirmed")
}

// applyAPIUserPassword sets a new password on the standardized API user
// directly on the router.
func (s *RouterService) applyAPIUserPassword(routerID uint, username, password string) error {
	if username == "" {
		username = STANDARD_API_USERNAME
	}

	reply, err := s.RunRouterCommand(routerID, "/user/print", "=name="+username)
	if err != nil {
		return err
	}
	if reply == nil || len(reply.Re) == 0 {
		return fmt.Errorf("the standardized api user %q is not present on the router", username)
	}

	id := reply.Re[0].Map[".id"]
	if id == "" {
		return fmt.Errorf("the standardized api user %q has no id on the router", username)
	}

	if _, err := s.RunRouterCommand(routerID, "/user/set", "=numbers="+id, "=password="+password); err != nil {
		return fmt.Errorf("failed to set the password for %q: %w", username, err)
	}

	return nil
}

// -----------------------------------------------------------------------
// Hotspot user management (operator facing)
// -----------------------------------------------------------------------

// ListHotspotUsersHandler handles GET /router/:id/hotspot/users.
func (s *RouterService) ListHotspotUsersHandler(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	if _, err := s.ownedRouter(userID, id); err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	users, err := s.ListHotspotUsers(id)
	if err != nil {
		return base.APIError(c, fiber.StatusBadGateway, err.Error())
	}

	return base.OK(c, HotspotUserListResponse{Users: users}, "hotspot users fetched")
}

// ProvisionHotspotUserHandler handles POST /router/:id/hotspot/users.
//
// It provisions a hotspot user from a package, or from an existing
// voucher code when VoucherCode is supplied.
func (s *RouterService) ProvisionHotspotUserHandler(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	var body struct {
		ProvisionHotspotUserRequest
		VoucherCode string `json:"voucherCode"`
	}
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	req := body.ProvisionHotspotUserRequest

	// Provisioning from a voucher code: look the voucher up, take its
	// package and its credentials, and mark it as provisioned.
	if body.VoucherCode != "" {
		var voucher vouchers.Voucher
		if err := s.DB.Preload("Package").Where("username = ? AND user_id = ?", body.VoucherCode, userID).First(&voucher).Error; err != nil {
			return base.APIError(c, fiber.StatusNotFound, "voucher not found")
		}
		if voucher.Provisioned {
			return base.APIError(c, fiber.StatusConflict, "voucher is already provisioned on a router")
		}
		if req.Username == "" {
			req.Username = voucher.Username
		}
		if req.Comment == "" {
			req.Comment = "voucher " + voucher.Username
		}

		pkg := voucher.Package
		user, provErr := s.ProvisionHotspotUser(id, req, pkg)
		if provErr != nil {
			return base.APIError(c, fiber.StatusBadGateway, provErr.Error())
		}

		now := time.Now()
		if err := s.DB.Model(&vouchers.Voucher{}).Where("id = ?", voucher.ID).Updates(map[string]interface{}{
			"provisioned":    true,
			"provisioned_at": now,
			"router_id":      rtr.ID,
		}).Error; err != nil {
			return base.APIError(c, fiber.StatusInternalServerError, "failed to record the provisioning")
		}

		return base.OK(c, user, "hotspot user provisioned")
	}

	if req.Username == "" {
		return base.APIError(c, fiber.StatusBadRequest, "username or voucherCode is required")
	}

	var pkg *packages.Package
	if req.PackageID != nil && *req.PackageID != 0 {
		var p packages.Package
		if err := s.DB.Where("id = ? AND user_id = ?", *req.PackageID, userID).First(&p).Error; err != nil {
			return base.APIError(c, fiber.StatusNotFound, "package not found")
		}
		pkg = &p
	}

	user, provErr := s.ProvisionHotspotUser(id, req, pkg)
	if provErr != nil {
		return base.APIError(c, fiber.StatusBadGateway, provErr.Error())
	}

	return base.OK(c, user, "hotspot user provisioned")
}

// RemoveHotspotUserHandler handles DELETE /router/:id/hotspot/users/:username.
func (s *RouterService) RemoveHotspotUserHandler(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	if _, err := s.ownedRouter(userID, id); err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	username := c.Params("username")
	if username == "" {
		return base.APIError(c, fiber.StatusBadRequest, "username is required")
	}

	if err := s.RemoveHotspotUser(id, username); err != nil {
		return base.APIError(c, fiber.StatusBadGateway, err.Error())
	}

	// Keep the voucher's provisioning state in sync with the router.
	if err := s.DB.Model(&vouchers.Voucher{}).
		Where("username = ? AND user_id = ?", username, userID).
		Updates(map[string]interface{}{"provisioned": false, "router_id": nil}).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to update the voucher")
	}

	return base.OK(c, nil, "hotspot user removed")
}

// -----------------------------------------------------------------------
// Remote access
// -----------------------------------------------------------------------

// GetRemoteAccessHandler handles GET /router/:id/remote-access.
func (s *RouterService) GetRemoteAccessHandler(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	state, err := s.GetRemoteAccess(id, rtr.RemoteAccess)
	if err != nil {
		return base.APIError(c, fiber.StatusBadGateway, err.Error())
	}

	return base.OK(c, state, "remote access state fetched")
}

// SetRemoteAccessHandler handles POST /router/:id/remote-access.
func (s *RouterService) SetRemoteAccessHandler(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}
	id, idErr := routerParam(c)
	if idErr != nil {
		return base.APIError(c, fiber.StatusBadRequest, idErr.Error())
	}
	rtr, err := s.ownedRouter(userID, id)
	if err != nil {
		return base.APIError(c, fiber.StatusNotFound, "router not found")
	}

	var body SetRemoteAccessRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	state, err := s.SetRemoteAccess(id, body.Enabled)
	if err != nil {
		return base.APIError(c, fiber.StatusBadGateway, err.Error())
	}

	if err := s.DB.Model(&rtr).Update("remote_access", body.Enabled).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to record the remote access state")
	}

	return base.OK(c, state, "remote access updated")
}
