package config

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type Dependencies struct {
	DB  *gorm.DB
	App *fiber.App
}
