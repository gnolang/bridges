package main

import "testing"

// Every machine's command is registered (the other lane's two by their own files), and a status endpoint listens on
// a loopback address only (G16).
func TestCommandsAndLoopback(t *testing.T) {
	for _, name := range []string{"updater", "relayer", "signer", "monitor"} {
		if commands[name] == nil {
			t.Errorf("no %s command", name)
		}
	}
	for addr, want := range map[string]bool{"127.0.0.1:9201": true, "localhost:9203": true, "[::1]:9201": true, "0.0.0.0:9201": false, "10.0.0.5:9201": false, ":9201": false} {
		if got := loopback(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}
