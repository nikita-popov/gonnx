// export_test.go exposes internal helpers for white-box testing.
// This file is compiled only during `go test`.
package runtime

import "net/http"

// InjectWorker inserts a pre-built ready Worker into the Manager's map.
// Used by tests to bypass os/exec when testing proxy logic.
func InjectWorker(m *Manager, name, sock string) {
	transport := &http.Transport{
		DialContext: unixDialer(sock),
	}
	w := &Worker{
		BundleName: name,
		SocketPath: sock,
		cmd:        nil,
		client:     &http.Client{Transport: transport},
		state:      StateReady,
	}
	m.mu.Lock()
	m.workers[name] = w
	m.mu.Unlock()
}
