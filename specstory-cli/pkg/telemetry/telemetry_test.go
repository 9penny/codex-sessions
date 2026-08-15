package telemetry

import (
	"net"
	"testing"
	"time"
)

func TestEndpointReachable(t *testing.T) {
	// Real listener for the reachable case.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	liveAddr := listener.Addr().String()

	// WSL can transiently report a just-released ephemeral port as reachable through its
	// localhost forwarding layer. Port 1 avoids that ephemeral range and is not used by the
	// test environment, making the archived reachability test stable on Linux and WSL.
	closedAddr := "127.0.0.1:1"

	tests := []struct {
		name string
		host string
		want bool
	}{
		{
			name: "listening endpoint is reachable",
			host: liveAddr,
			want: true,
		},
		{
			name: "closed port is unreachable",
			host: closedAddr,
			want: false,
		},
		{
			name: "malformed host is unreachable",
			host: "not a host:port",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got := endpointReachable(tt.host)
			elapsed := time.Since(start)

			if got != tt.want {
				t.Errorf("endpointReachable(%q) = %v, want %v", tt.host, got, tt.want)
			}
			// The probe must never block much past its timeout — that would
			// reintroduce the startup stall this check exists to prevent.
			if elapsed > endpointDialTimeout+200*time.Millisecond {
				t.Errorf("endpointReachable(%q) took %v, expected under %v", tt.host, elapsed, endpointDialTimeout)
			}
		})
	}
}
