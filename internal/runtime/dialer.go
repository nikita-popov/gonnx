package runtime

import (
	"context"
	"net"
)

// unixDialer returns a DialContext func that always connects to the given
// Unix domain socket path, ignoring the network and address arguments.
func unixDialer(sock string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}
}
