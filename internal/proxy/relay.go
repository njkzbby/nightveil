package proxy

import (
	"io"
	"sync"
)

// Relay copies data bidirectionally between left and right.
// Returns when either direction hits an error or EOF.
// Closes both sides when done.
func Relay(left, right io.ReadWriteCloser) error {
	var once sync.Once
	closeAll := func() {
		left.Close()
		right.Close()
	}

	var errLeft, errRight error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, errRight = io.Copy(right, left)
		once.Do(closeAll) // EOF or error → close both to unblock the other goroutine
	}()

	go func() {
		defer wg.Done()
		_, errLeft = io.Copy(left, right)
		once.Do(closeAll)
	}()

	wg.Wait()

	if errLeft != nil {
		return errLeft
	}
	return errRight
}
