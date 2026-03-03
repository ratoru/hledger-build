//go:build !windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
)

// notifySignals registers the OS signals that should trigger a graceful
// shutdown. On Unix we catch both SIGINT (Ctrl-C) and SIGTERM (process
// manager / kill).
func notifySignals(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
}
