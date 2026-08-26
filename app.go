package main

import (
	"log"
	"os"

	"github.com/kigongo-vincent/hotspot-be/config"
)

func main() {
	// 1. Build the application setup
	app := config.SetupApplication()

	db := config.ConnectDB()
	config.RegisterAppRoutes(config.Dependencies{DB: db, App: app})

	// 2. Start the server here for production running
	// err := app.Listen(fmt.Sprintf(":%s", os.Getenv("APP_PORT")))
	// if err != nil {
	// 	log.Fatal("failed to start server, check root file")
	// }

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // fallback for local dev
	}
	err := app.Listen(":" + port)
	if err != nil {
		log.Fatal("failed to start server, check root file")
	}
}
