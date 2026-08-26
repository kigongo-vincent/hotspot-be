package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/joho/godotenv"
	"github.com/kigongo-vincent/hotspot-be/modules/auth"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func RegisterAppRoutes(dependecies Dependencies) {

	v1 := dependecies.App.Group("/api/v1")

	// health check
	v1.Get("/health", func(c *fiber.Ctx) error {
		fmt.Println("health hit!")
		return c.SendStatus(fiber.StatusOK)

	})

	register := base.RouteRegister{DB: dependecies.DB, Router: v1}
	auth.RouterRegister(&register)
}

func SetupApplication() *fiber.App {
	_, filename, _, _ := runtime.Caller(0)
	configDir := filepath.Dir(filename)

	rootEnvPath := filepath.Join(configDir, "../.env")

	_ = godotenv.Load(rootEnvPath)

	app := fiber.New()
	// app.Use(limiter.New(limiter.Config{Max: 100, Expiration: 60 * time.Second}))
	app.Use(cors.New())
	return app
}

func ConnectDB() *gorm.DB {

	dsn := fmt.Sprintf("host=%s port=%s dbname=%s password=%s user=%s sslmode=%s",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_SSLMODE"),
	)

	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{

		Logger: logger.Default.LogMode(logger.Info),
	})
	db.AutoMigrate(
		&shared.User{},
		&shared.Business{},
	)
	if err != nil {
		log.Fatal("failed to start application due to db connection failure")

	}

	return db

}
