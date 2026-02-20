package service

import (
	"os"
	"testing"

	"github.com/ayo6706/payment-multicurrency/internal/testutil/dblock"
)

func TestMain(m *testing.M) {
	release := dblock.Acquire()
	code := m.Run()
	release()
	os.Exit(code)
}
