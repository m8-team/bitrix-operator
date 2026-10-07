package main

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestDenialCannotPassOnBrokenFixture(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"timeout", os.ErrDeadlineExceeded, 3}, {"refused", errors.New("connection refused"), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := connect("127.0.0.1:8080", func(_, _ string, _ time.Duration) (net.Conn, error) { return nil, tc.err })
			if got != tc.want {
				t.Fatalf("exit=%d, want %d", got, tc.want)
			}
		})
	}
	for _, payload := range []string{reply, "broken"} {
		got := connect("127.0.0.1:8080", func(_, _ string, _ time.Duration) (net.Conn, error) {
			client, server := net.Pipe()
			go func() { _, _ = io.WriteString(server, payload); _ = server.Close() }()
			return client, nil
		})
		want := 5
		if payload == reply {
			want = 0
		}
		if got != want {
			t.Fatalf("handshake exit=%d, want %d", got, want)
		}
	}
	if got := connect("service.invalid:8080", net.DialTimeout); got != 2 {
		t.Fatal("DNS failures can masquerade as policy drops")
	}
}
