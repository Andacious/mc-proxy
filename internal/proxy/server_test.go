package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
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

func TestUDPProxyMaintainsIndependentClientSessions(t *testing.T) {
	upstream := startEchoServer(t)
	current := startProxy(t, upstream.LocalAddr().String(), time.Second)

	for i := 0; i < 2; i++ {
		client := dialProxy(t, current)
		defer client.Close()
		assertEcho(t, client, "client-"+strconv.Itoa(i))
	}

	current.mu.Lock()
	sessionCount := len(current.sessions)
	current.mu.Unlock()
	if sessionCount != 2 {
		t.Fatalf("session count = %d, want 2", sessionCount)
	}
}

func TestUDPProxyHandles25ConcurrentClients(t *testing.T) {
	const (
		clientCount      = 25
		packetsPerClient = 40
		maxP95Latency    = time.Second
		maxTestDuration  = 10 * time.Second
	)

	upstream := startEchoServer(t)
	current := startProxy(t, upstream.LocalAddr().String(), 30*time.Second)
	clients := make([]*net.UDPConn, clientCount)
	for i := range clients {
		clients[i] = dialProxy(t, current)
		t.Cleanup(func() { clients[i].Close() })
	}

	start := make(chan struct{})
	results := make(chan packetResult, clientCount*packetsPerClient)
	var workers sync.WaitGroup
	workers.Add(clientCount)
	testStart := time.Now()
	for clientID, client := range clients {
		go func() {
			defer workers.Done()
			<-start
			for sequence := 0; sequence < packetsPerClient; sequence++ {
				payload := []byte(fmt.Sprintf("client=%d sequence=%d", clientID, sequence))
				started := time.Now()
				if err := roundTrip(client, payload, 2*time.Second); err != nil {
					results <- packetResult{err: fmt.Errorf("client %d packet %d: %w", clientID, sequence, err)}
					return
				}
				results <- packetResult{latency: time.Since(started)}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var latencies []time.Duration
	for result := range results {
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		latencies = append(latencies, result.latency)
	}
	if t.Failed() {
		return
	}

	wantPackets := clientCount * packetsPerClient
	if len(latencies) != wantPackets {
		t.Fatalf("completed packets = %d, want %d", len(latencies), wantPackets)
	}
	elapsed := time.Since(testStart)
	if elapsed > maxTestDuration {
		t.Fatalf("test duration = %s, want at most %s", elapsed, maxTestDuration)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)*95+99)/100-1]
	if p95 > maxP95Latency {
		t.Fatalf("p95 latency = %s, want at most %s", p95, maxP95Latency)
	}

	current.mu.Lock()
	sessionCount := len(current.sessions)
	current.mu.Unlock()
	if sessionCount != clientCount {
		t.Fatalf("session count = %d, want %d", sessionCount, clientCount)
	}
	t.Logf("forwarded %d packets for %d clients in %s (p95 %s)", wantPackets, clientCount, elapsed, p95)
}

func TestUDPProxyRemovesIdleSessions(t *testing.T) {
	upstream := startEchoServer(t)
	current := startProxy(t, upstream.LocalAddr().String(), 30*time.Millisecond)
	client := dialProxy(t, current)
	defer client.Close()
	assertEcho(t, client, "expire me")

	waitFor(t, time.Second, func() bool {
		current.mu.Lock()
		defer current.mu.Unlock()
		return len(current.sessions) == 0
	})
}

func TestGetSessionReturnsTargetResolutionError(t *testing.T) {
	current, err := newUDPProxy(config.Mapping{
		Domain:      "featured.example.com.",
		Listen:      "127.0.0.1:0",
		Target:      "missing-port",
		IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("newUDPProxy() error = %v", err)
	}
	defer current.close()

	_, err = current.getSession(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 30000})
	if err == nil {
		t.Fatal("getSession() error = nil, want target resolution error")
	}
}

func TestNewAllClosesPreviouslyOpenedListenersOnFailure(t *testing.T) {
	address := unusedUDPAddress(t)
	_, err := NewAll([]config.Mapping{
		{Domain: "one.example.com.", Listen: address, Target: "127.0.0.1:19132", IdleTimeout: time.Second},
		{Domain: "two.example.com.", Listen: "missing-port", Target: "127.0.0.1:19133", IdleTimeout: time.Second},
	})
	if err == nil {
		t.Fatal("NewAll() error = nil, want listener resolution error")
	}

	listener, err := net.ListenUDP("udp", mustResolveUDP(t, address))
	if err != nil {
		t.Fatalf("first listener was not closed after failure: %v", err)
	}
	listener.Close()
}

func TestSetRunStopsAllProxiesAfterCancellation(t *testing.T) {
	set, err := NewAll([]config.Mapping{
		{Domain: "one.example.com.", Listen: "127.0.0.1:0", Target: "127.0.0.1:19132", IdleTimeout: time.Second},
		{Domain: "two.example.com.", Listen: "127.0.0.1:0", Target: "127.0.0.1:19133", IdleTimeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewAll() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- set.Run(ctx)
	}()
	cancel()

	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func BenchmarkUDPProxyConcurrentClients(b *testing.B) {
	for _, clientCount := range []int{20, 100} {
		b.Run(strconv.Itoa(clientCount)+"Clients", func(b *testing.B) {
			upstream := startEchoServer(b)
			current := startProxy(b, upstream.LocalAddr().String(), time.Minute)
			clients := make([]*net.UDPConn, clientCount)
			for i := range clients {
				clients[i] = dialProxy(b, current)
				b.Cleanup(func() { clients[i].Close() })
			}

			payload := bytes.Repeat([]byte{0xab}, 512)
			errs := make(chan error, clientCount)
			var workers sync.WaitGroup
			workers.Add(clientCount)
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for clientID, client := range clients {
				go func() {
					defer workers.Done()
					for sequence := clientID; sequence < b.N; sequence += clientCount {
						if err := roundTrip(client, payload, 5*time.Second); err != nil {
							errs <- fmt.Errorf("client %d packet %d: %w", clientID, sequence, err)
							return
						}
					}
				}()
			}
			workers.Wait()
			b.StopTimer()
			close(errs)
			for err := range errs {
				b.Error(err)
			}
		})
	}
}

type packetResult struct {
	latency time.Duration
	err     error
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

func startEchoServer(t testing.TB) *net.UDPConn {
	t.Helper()
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP(upstream) error = %v", err)
	}
	t.Cleanup(func() { upstream.Close() })
	go echo(upstream)
	return upstream
}

func startProxy(t testing.TB, target string, idleTimeout time.Duration) *UDPProxy {
	t.Helper()
	current, err := newUDPProxy(config.Mapping{
		Domain:      "featured.example.com.",
		Listen:      "127.0.0.1:0",
		Target:      target,
		IdleTimeout: idleTimeout,
	})
	if err != nil {
		t.Fatalf("newUDPProxy() error = %v", err)
	}
	t.Cleanup(current.close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := current.run(ctx); err != nil {
			t.Errorf("run() error = %v", err)
		}
	}()
	return current
}

func dialProxy(t testing.TB, current *UDPProxy) *net.UDPConn {
	t.Helper()
	client, err := net.DialUDP("udp", nil, current.listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP(proxy) error = %v", err)
	}
	return client
}

func assertEcho(t *testing.T, client *net.UDPConn, message string) {
	t.Helper()
	if err := roundTrip(client, []byte(message), time.Second); err != nil {
		t.Fatalf("roundTrip() error = %v", err)
	}
}

func roundTrip(client *net.UDPConn, payload []byte, timeout time.Duration) error {
	if _, err := client.Write(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	buffer := make([]byte, len(payload)+1)
	size, err := client.Read(buffer)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(buffer[:size], payload) {
		return fmt.Errorf("response did not match %d-byte request", len(payload))
	}
	return nil
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func unusedUDPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	address := listener.LocalAddr().String()
	listener.Close()
	return address
}

func mustResolveUDP(t *testing.T, address string) *net.UDPAddr {
	t.Helper()
	resolved, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatalf("ResolveUDPAddr() error = %v", err)
	}
	return resolved
}
