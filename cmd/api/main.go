package main

import (
	"log"

	"github.com/ayo6706/payment-multicurrency/internal/db"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load() // Load .env file if present

	log.Println("Starting Payment System...")

	// Initialize Database Connection
	pool, err := db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	log.Println("Connected to Database successfully!")

}
