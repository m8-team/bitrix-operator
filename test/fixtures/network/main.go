// netprobe is a synthetic test server/client, not a Bitrix runtime component.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const reply = "bitrix-network-fixture\n"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "serve" {
		fmt.Fprintln(os.Stderr, serve())
		os.Exit(2)
	}
	if len(os.Args) == 3 && os.Args[1] == "connect" {
		os.Exit(connect(os.Args[2], net.DialTimeout))
	}
	if len(os.Args) == 4 && os.Args[1] == "dns" {
		if err := resolve(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: netprobe serve | connect IP:port | dns IP:53 udp|tcp")
	os.Exit(2)
}

func serve() error {
	for _, port := range []string{"8080", "8081", "3306", "5432", "6379", "8010", "9010", "8443"} {
		listener, err := net.Listen("tcp4", ":"+port)
		if err != nil {
			return err
		}
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
				_, _ = io.WriteString(conn, reply)
				_ = conn.Close()
			}
		}()
	}
	select {}
}

// Only a dial timeout to a numeric IP means a possible network-policy drop.
// Refused connections, broken responders, DNS and kubectl failures must not pass denial tests.
func connect(address string, dial func(string, string, time.Duration) (net.Conn, error)) int {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return 2
	}
	conn, err := dial("tcp4", address, 2*time.Second)
	if err != nil {
		if os.IsTimeout(err) {
			return 3
		}
		return 4
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return 5
	}
	data := make([]byte, len(reply))
	if _, err := io.ReadFull(conn, data); err != nil || string(data) != reply {
		return 5
	}
	return 0
}

func resolve(address, network string) error {
	if network != "udp" && network != "tcp" {
		return fmt.Errorf("unsupported DNS transport")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return fmt.Errorf("DNS server must be a numeric IP:port")
	}
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = resolver.LookupHost(ctx, "kubernetes.default.svc.cluster.local")
	return err
}
