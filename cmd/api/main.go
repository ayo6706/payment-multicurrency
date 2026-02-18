package main

import (
	"log"

	"github.com/ayo6706/payment-multicurrency/internal/db"
)

func main() {
	log.Println("Starting Payment System...")

	// Initialize Database Connection
	pool, err := db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	log.Println("Connected to Database successfully!")

}
