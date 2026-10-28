package users

import (
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/gorm"
)

type UserService struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *UserService {
	return &UserService{DB: db}
}

func toResponse(u shared.User) UserResponse {
	return UserResponse{
		ID:    u.ID,
		Name:  u.Name,
		Email: u.Email,
		Phone: u.Phone,
		Role:  string(u.Role),
	}
}

// apiSuccess wraps a success payload in the envelope the frontend's API
// client expects: { msg, data }.
func apiSuccess(c *fiber.Ctx, msg string, data any) error {
	return c.JSON(fiber.Map{
		"msg":  msg,
		"data": data,
	})
}

// findMember locates a member of the given company by its ID (as passed
// in the route param), returning an error if no member matches.
func findMember(company shared.Company, idParam string) (shared.User, error) {
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return shared.User{}, errors.New("invalid id")
	}

	for _, m := range company.Members {
		if uint64(m.ID) == id {
			return m, nil
		}
	}

	return shared.User{}, errors.New("member not found")
}

// companyForOwner loads the calling user's company, along with its
// members, or returns an error if the caller doesn't own one.
func (s *UserService) companyForOwner(ownerID uint) (shared.Company, error) {
	var company shared.Company
	err := s.DB.Preload("Members").Where("user_id = ?", ownerID).First(&company).Error
	return company, err
}

// FindAll returns the members of the authenticated user's company.
func (s *UserService) FindAll(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	company, err := s.companyForOwner(ownerID)
	if err != nil {
		return base.API_ERROR(c, "company not found")
	}

	resp := make([]UserResponse, 0, len(company.Members))
	for _, m := range company.Members {
		resp = append(resp, toResponse(m))
	}

	return apiSuccess(c, "members fetched", UserListResponse{Users: resp})
}

// FindByID returns a single member, scoped to the authenticated user's
// company so one company's owner can't look up another company's member.
func (s *UserService) FindByID(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	company, err := s.companyForOwner(ownerID)
	if err != nil {
		return base.API_ERROR(c, "company not found")
	}

	member, mErr := findMember(company, c.Params("id"))
	if mErr != nil {
		return base.API_ERROR(c, "member not found")
	}

	return apiSuccess(c, "member fetched", toResponse(member))
}

// Create adds a member to the authenticated user's company. Since every
// user authenticates via Google, this never sets a password — it either
// attaches an existing User row matching the given email, or creates a
// bare (password-less) one and attaches that.
func (s *UserService) Create(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var company shared.Company
	if err := s.DB.Where("user_id = ?", ownerID).First(&company).Error; err != nil {
		return base.API_ERROR(c, "company not found")
	}

	var body UserRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.Email == "" {
		return base.API_ERROR(c, "email is required")
	}

	var member shared.User
	err := s.DB.Where("email = ?", body.Email).First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		member = shared.User{
			Name:  body.Name,
			Email: body.Email,
			Phone: body.Phone,
			Role:  shared.UserRole(body.Role),
		}
		if err := s.DB.Create(&member).Error; err != nil {
			return base.API_ERROR(c, "failed to create member")
		}
	} else if err != nil {
		return base.API_ERROR(c, "failed to look up member")
	}

	if err := s.DB.Model(&company).Association("Members").Append(&member); err != nil {
		return base.API_ERROR(c, "failed to add member to company")
	}

	return apiSuccess(c, "member added", toResponse(member))
}

// Update overwrites an existing member's fields, scoped to members of the
// authenticated user's company.
func (s *UserService) Update(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	company, err := s.companyForOwner(ownerID)
	if err != nil {
		return base.API_ERROR(c, "company not found")
	}

	member, mErr := findMember(company, c.Params("id"))
	if mErr != nil {
		return base.API_ERROR(c, "member not found")
	}

	var body UserRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	member.Name = body.Name
	member.Email = body.Email
	member.Phone = body.Phone
	member.Role = shared.UserRole(body.Role)

	if err := s.DB.Save(&member).Error; err != nil {
		return base.API_ERROR(c, "failed to update member")
	}

	return apiSuccess(c, "member updated", toResponse(member))
}

// Delete removes a member from the authenticated user's company. This
// detaches the membership rather than deleting the underlying User row,
// since the same Google-authenticated user may belong to other companies.
func (s *UserService) Delete(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var company shared.Company
	if err := s.DB.Preload("Members").Where("user_id = ?", ownerID).First(&company).Error; err != nil {
		return base.API_ERROR(c, "company not found")
	}

	member, mErr := findMember(company, c.Params("id"))
	if mErr != nil {
		return base.API_ERROR(c, "member not found")
	}

	if err := s.DB.Model(&company).Association("Members").Delete(&member); err != nil {
		return base.API_ERROR(c, "failed to remove member")
	}

	return apiSuccess(c, "member removed", nil)
}

// DeleteMany removes several members at once — backs the toolbar's
// "delete selected" bulk action. Scoped so an owner can only remove
// members that actually belong to their company.
func (s *UserService) DeleteMany(c *fiber.Ctx) error {
	ownerID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var company shared.Company
	if err := s.DB.Preload("Members").Where("user_id = ?", ownerID).First(&company).Error; err != nil {
		return base.API_ERROR(c, "company not found")
	}

	var body DeleteManyRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if len(body.IDs) == 0 {
		return base.API_ERROR(c, "no ids provided")
	}

	idSet := make(map[uint]bool, len(body.IDs))
	for _, id := range body.IDs {
		idSet[id] = true
	}

	toRemove := make([]shared.User, 0, len(body.IDs))
	for _, m := range company.Members {
		if idSet[m.ID] {
			toRemove = append(toRemove, m)
		}
	}

	if len(toRemove) == 0 {
		return apiSuccess(c, "members removed", nil)
	}

	if err := s.DB.Model(&company).Association("Members").Delete(toRemove); err != nil {
		return base.API_ERROR(c, "failed to remove members")
	}

	return apiSuccess(c, "members removed", nil)
}
