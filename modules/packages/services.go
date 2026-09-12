package packages

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type PackageService struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *PackageService {
	return &PackageService{DB: db}
}

func toResponse(p Package) PackageResponse {
	return PackageResponse{
		ID:         p.ID,
		Name:       p.Name,
		Price:      strconv.Itoa(datatypes.NewJSONType((p.Price)).Data().Data().Value),
		Duration:   p.Duration,
		DataLimit:  p.DataLimit,
		SpeedLimit: p.SpeedLimit,
		IsActive:   p.IsActive,
	}
}

// FindAll returns every package owned by the authenticated user, most
// recently created first.
func (s *PackageService) FindAll(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var pkgs []Package
	if err := s.DB.Where("user_id = ?", userID).Order("id desc").Find(&pkgs).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch packages")
	}

	resp := make([]PackageResponse, 0, len(pkgs))
	for _, p := range pkgs {
		resp = append(resp, toResponse(p))
	}

	return c.JSON(base.APIResponse{Data: PackageListResponse{Packages: resp}})
}

// FindByID returns a single package, scoped to the authenticated user so
// one user can't fetch another user's package by guessing an ID.
func (s *PackageService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var pkg Package
	if err := s.DB.Where("user_id = ?", userID).First(&pkg, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "package not found")
	}

	return c.JSON(base.APIResponse{Data: toResponse(pkg)})
}

// Create adds a new package owned by the authenticated user. UserID is
// always taken from the token, never from the request body.
func (s *PackageService) Create(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var body PackageRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.Name == "" {
		return base.API_ERROR(c, "name is required")
	}

	price, pErr := strconv.Atoi(body.Price)
	if pErr != nil {

	}

	pkg := Package{
		Name:       body.Name,
		Price:      datatypes.NewJSONType(Price{Value: price, Currency: "UGX"}),
		Duration:   body.Duration,
		DataLimit:  body.DataLimit,
		SpeedLimit: body.SpeedLimit,
		IsActive:   body.IsActive,
		UserID:     &userID,
	}

	if err := s.DB.Create(&pkg).Error; err != nil {
		return base.API_ERROR(c, "failed to create package")
	}

	return c.JSON(base.APIResponse{Data: toResponse(pkg)})
}

// Update overwrites an existing package's fields, scoped to packages owned
// by the authenticated user.
func (s *PackageService) Update(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var pkg Package
	if err := s.DB.Where("user_id = ?", userID).First(&pkg, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "package not found")
	}

	var body PackageRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	price, pErr := strconv.Atoi(body.Price)
	if pErr != nil {

	}
	pkg.Name = body.Name
	pkg.Price = datatypes.NewJSONType(Price{Value: price, Currency: "UGX"})
	pkg.Duration = body.Duration
	pkg.DataLimit = body.DataLimit
	pkg.SpeedLimit = body.SpeedLimit
	pkg.IsActive = body.IsActive

	if err := s.DB.Save(&pkg).Error; err != nil {
		return base.API_ERROR(c, "failed to update package")
	}

	return c.JSON(base.APIResponse{Data: toResponse(pkg)})
}

// ToggleActive flips (sets) a package's active status without requiring
// the full form payload — used by the table's inline Switch. Scoped to
// packages owned by the authenticated user.
func (s *PackageService) ToggleActive(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var pkg Package
	if err := s.DB.Where("user_id = ?", userID).First(&pkg, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "package not found")
	}

	var body ToggleActiveRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	pkg.IsActive = body.IsActive
	if err := s.DB.Save(&pkg).Error; err != nil {
		return base.API_ERROR(c, "failed to update package")
	}

	return c.JSON(base.APIResponse{Data: toResponse(pkg)})
}

// Delete removes a single package, scoped to packages owned by the
// authenticated user.
func (s *PackageService) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var pkg Package
	if err := s.DB.Where("user_id = ?", userID).First(&pkg, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "package not found")
	}

	if err := s.DB.Delete(&pkg).Error; err != nil {
		return base.API_ERROR(c, "failed to delete package")
	}

	return c.SendStatus(200)
}

// DeleteMany removes several packages at once — backs the toolbar's
// "delete selected" bulk action. Scoped so a user can only delete IDs
// they actually own, even if other IDs are included in the request.
func (s *PackageService) DeleteMany(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var body DeleteManyRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if len(body.IDs) == 0 {
		return base.API_ERROR(c, "no ids provided")
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&Package{}, body.IDs).Error; err != nil {
		return base.API_ERROR(c, "failed to delete packages")
	}

	return c.SendStatus(200)
}
