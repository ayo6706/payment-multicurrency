package dblock

import (
	"net"
	"time"
)

const lockAddr = "127.0.0.1:45432"

func Acquire() func() {
	for {
		ln, err := net.Listen("tcp", lockAddr)
		if err == nil {
			return func() { ln.Close() }
		}
		time.Sleep(50 * time.Millisecond)
	}
}
