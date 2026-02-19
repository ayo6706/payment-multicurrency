package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ayo6706/payment-multicurrency/internal/api"
	"github.com/ayo6706/payment-multicurrency/internal/db"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	log.Println("Starting Payment System...")

	// Initialize Database Connection
	pool, err := db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	log.Println("Connected to Database successfully!")

	// Initialize Repository
	repo := repository.NewRepository(pool)

	// Initialize Router
	router := api.NewRouter(pool, repo)

	// Start Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, router.Routes()); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
