package signals

import (
	"os"
	"os/signal"
)

//Shutdown ...
func Shutdown(stop func()) {
	sig := make(chan os.Signal, 2)
	signal.Notify(
		sig,
		shutdownSignals...,
	)
	go func() {
		<-sig
		go stop()
		<-sig
		os.Exit(1) // second signal. Exit directly.
	}()
}
