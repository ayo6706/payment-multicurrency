package main

import (
	"log"

	"github.com/ayo6706/payment-multicurrency/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatalf("application error: %v", err)
	}
}
