package main

import (
	"fmt"
	"os"

	"github.com/ayo6706/payment-multicurrency/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}
