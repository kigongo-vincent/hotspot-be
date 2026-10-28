package settings

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"gorm.io/gorm"
)

// SettingsService reads and writes the tenant's configuration.
type SettingsService struct {
	DB *gorm.DB
}

// NewService builds the settings service.
func NewService(db *gorm.DB) *SettingsService {
	return &SettingsService{DB: db}
}

// List handles GET /settings. Returns the resolved settings map - every
// catalogue key is present, using the stored value where one exists and
// the catalogue default otherwise - alongside the catalogue itself.
func (s *SettingsService) List(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var stored []Setting
	if err := s.DB.Where("user_id = ?", userID).Find(&stored).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to fetch settings")
	}

	values := make(map[string]string, len(KnownSettings))
	for _, def := range KnownSettings {
		values[def.Key] = def.Default
	}
	for _, setting := range stored {
		values[setting.Key] = setting.Value
	}

	return base.OK(c, SettingResponse{Settings: values, Defined: KnownSettings}, "settings fetched")
}

// Catalogue handles GET /settings/catalogue.
func (s *SettingsService) Catalogue(c *fiber.Ctx) error {
	return base.OK(c, KnownSettings, "settings catalogue fetched")
}

// Upsert handles PUT /settings. Creates the setting when it does not
// exist yet, updates it otherwise.
func (s *SettingsService) Upsert(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var body SettingUpsertRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}

	key := strings.TrimSpace(body.Key)
	if key == "" {
		return base.APIError(c, fiber.StatusBadRequest, "key is required")
	}

	if err := s.write(userID, key, body.Value); err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, map[string]string{key: body.Value}, "setting saved")
}

// BulkUpsert handles POST /settings/bulk. Used by the settings page,
// which saves a whole tab at once.
func (s *SettingsService) BulkUpsert(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var body BulkUpsertRequest
	if err := c.BodyParser(&body); err != nil {
		return base.APIError(c, fiber.StatusBadRequest, "failed to parse input")
	}
	if len(body.Settings) == 0 {
		return base.APIError(c, fiber.StatusBadRequest, "no settings provided")
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for key, value := range body.Settings {
			if err := s.writeWith(tx, userID, strings.TrimSpace(key), value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, err.Error())
	}

	return base.OK(c, body.Settings, "settings saved")
}

// Delete handles DELETE /settings/:key. The key falls back to its
// catalogue default once removed.
func (s *SettingsService) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	key := strings.TrimSpace(c.Params("key"))
	if key == "" {
		return base.APIError(c, fiber.StatusBadRequest, "key is required")
	}

	if err := s.DB.Where("key = ? AND user_id = ?", key, userID).Delete(&Setting{}).Error; err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to delete setting")
	}

	return base.OK(c, nil, "setting removed")
}

// write persists a single setting.
func (s *SettingsService) write(userID uint, key, value string) error {
	return s.writeWith(s.DB, userID, key, value)
}

// writeWith persists a single setting on the given transaction handle.
func (s *SettingsService) writeWith(db *gorm.DB, userID uint, key, value string) error {
	var setting Setting
	err := db.Where("key = ? AND user_id = ?", key, userID).First(&setting).Error
	if err == nil {
		setting.Value = value
		return db.Save(&setting).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	return db.Create(&Setting{Key: key, Value: value, UserID: &userID}).Error
}
