//go:build windows

package runner

import (
	"os"
	"os/signal"
)

// notifySignals registers the OS signals that should trigger a graceful
// shutdown. On Windows only os.Interrupt (Ctrl-C) is available; SIGTERM
// does not exist.
func notifySignals(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt)
}
