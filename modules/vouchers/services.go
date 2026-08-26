package vouchers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func NewService(db *gorm.DB) *VoucherService {
	return &VoucherService{DB: db}
}

func (s *VoucherService) Create(c *fiber.Ctx) error {
	return nil
}
