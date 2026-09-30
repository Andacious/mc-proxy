package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
)

func TestUDPProxyForwardsInBothDirections(t *testing.T) {
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(upstream) error = %v", err)
	}
	defer upstream.Close()
	go echo(upstream)

	current, err := newUDPProxy(config.Mapping{
		Domain:      "featured.example.com.",
		Listen:      "127.0.0.1:0",
		Target:      upstream.LocalAddr().String(),
		IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("newUDPProxy() error = %v", err)
	}
	defer current.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		errs <- current.run(ctx)
	}()

	client, err := net.DialUDP("udp", nil, current.listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP(proxy) error = %v", err)
	}
	defer client.Close()

	want := []byte("bedrock packet")
	if _, err := client.Write(want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	got := make([]byte, 128)
	size, err := client.Read(got)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(got[:size]) != string(want) {
		t.Fatalf("response = %q, want %q", got[:size], want)
	}

	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not stop after cancellation")
	}
}

func echo(connection *net.UDPConn) {
	buffer := make([]byte, 64*1024)
	for {
		size, client, err := connection.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if _, err := connection.WriteToUDP(buffer[:size], client); err != nil {
			return
		}
	}
}
