package dnsserver

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
	"github.com/miekg/dns"
)

func TestServeDNSReturnsConfiguredAddress(t *testing.T) {
	server := New(
		config.DNSConfig{TTL: 90, Forwarders: []string{"1.1.1.1:53"}},
		[]config.Mapping{{
			Domain:  "featured.example.com.",
			ProxyIP: net.ParseIP("192.168.1.241").To4(),
		}},
	)
	request := new(dns.Msg)
	request.SetQuestion("FEATURED.EXAMPLE.COM.", dns.TypeA)
	writer := &recordingWriter{}

	server.ServeDNS(writer, request)

	if writer.message == nil {
		t.Fatal("ServeDNS() did not write a response")
	}
	if len(writer.message.Answer) != 1 {
		t.Fatalf("answer count = %d, want 1", len(writer.message.Answer))
	}
	answer, ok := writer.message.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer type = %T, want *dns.A", writer.message.Answer[0])
	}
	if answer.A.String() != "192.168.1.241" || answer.Hdr.Ttl != 90 {
		t.Fatalf("answer = %s TTL %d, want 192.168.1.241 TTL 90", answer.A, answer.Hdr.Ttl)
	}
	if !writer.message.Authoritative || !writer.message.RecursionAvailable {
		t.Fatal("mapped response is missing authoritative or recursion-available flags")
	}
}

func TestServeDNSReturnsNoAAAAForMappedDomain(t *testing.T) {
	server := New(
		config.DNSConfig{TTL: 90, Forwarders: []string{"1.1.1.1:53"}},
		[]config.Mapping{{
			Domain:  "featured.example.com.",
			ProxyIP: net.ParseIP("192.168.1.241").To4(),
		}},
	)
	request := new(dns.Msg)
	request.SetQuestion("featured.example.com.", dns.TypeAAAA)
	writer := &recordingWriter{}

	server.ServeDNS(writer, request)

	if writer.message == nil || writer.message.Rcode != dns.RcodeSuccess {
		t.Fatal("ServeDNS() did not return a successful response")
	}
	if len(writer.message.Answer) != 0 {
		t.Fatalf("answer count = %d, want 0", len(writer.message.Answer))
	}
}

func TestServeDNSForwardsUnmappedQueriesOverUDP(t *testing.T) {
	address, shutdown := startDNSServer(t, "udp", func(writer dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		response.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
			A:   net.ParseIP("203.0.113.10").To4(),
		}}
		if err := writer.WriteMsg(response); err != nil {
			t.Errorf("WriteMsg() error = %v", err)
		}
	})
	defer shutdown()

	server := New(config.DNSConfig{Forwarders: []string{address}}, nil)
	request := new(dns.Msg)
	request.SetQuestion("unmapped.example.com.", dns.TypeA)
	writer := &recordingWriter{network: "udp"}

	server.ServeDNS(writer, request)

	if len(writer.message.Answer) != 1 {
		t.Fatalf("answer count = %d, want 1", len(writer.message.Answer))
	}
	if got := writer.message.Answer[0].(*dns.A).A.String(); got != "203.0.113.10" {
		t.Fatalf("answer = %s, want 203.0.113.10", got)
	}
}

func TestServeDNSUsesTCPForTCPClients(t *testing.T) {
	address, shutdown := startDNSServer(t, "tcp", func(writer dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		if err := writer.WriteMsg(response); err != nil {
			t.Errorf("WriteMsg() error = %v", err)
		}
	})
	defer shutdown()

	server := New(config.DNSConfig{Forwarders: []string{address}}, nil)
	request := new(dns.Msg)
	request.SetQuestion("unmapped.example.com.", dns.TypeA)
	writer := &recordingWriter{network: "tcp"}

	server.ServeDNS(writer, request)

	if writer.message == nil || writer.message.Rcode != dns.RcodeSuccess {
		t.Fatal("ServeDNS() did not return the TCP forwarder's response")
	}
}

func TestServeDNSFallsBackToNextForwarder(t *testing.T) {
	address, shutdown := startDNSServer(t, "udp", func(writer dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		if err := writer.WriteMsg(response); err != nil {
			t.Errorf("WriteMsg() error = %v", err)
		}
	})
	defer shutdown()

	deadAddress := unusedUDPAddress(t)
	server := New(config.DNSConfig{Forwarders: []string{address, deadAddress}}, nil)
	server.timeout = 100 * time.Millisecond
	request := new(dns.Msg)
	request.SetQuestion("unmapped.example.com.", dns.TypeA)
	writer := &recordingWriter{network: "udp"}

	server.ServeDNS(writer, request)

	if writer.message == nil || writer.message.Rcode != dns.RcodeSuccess {
		t.Fatal("ServeDNS() did not use the healthy fallback forwarder")
	}
}

func TestServeDNSRetriesTruncatedUDPResponseOverTCP(t *testing.T) {
	tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenTCP() error = %v", err)
	}
	port := tcpListener.Addr().(*net.TCPAddr).Port
	udpConnection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		tcpListener.Close()
		t.Fatalf("ListenUDP() error = %v", err)
	}

	udpServer := &dns.Server{
		PacketConn: udpConnection,
		Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(request)
			response.Truncated = true
			if err := writer.WriteMsg(response); err != nil {
				t.Errorf("WriteMsg(UDP) error = %v", err)
			}
		}),
	}
	tcpServer := &dns.Server{
		Listener: tcpListener,
		Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(request)
			response.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET},
				A:   net.ParseIP("203.0.113.20").To4(),
			}}
			if err := writer.WriteMsg(response); err != nil {
				t.Errorf("WriteMsg(TCP) error = %v", err)
			}
		}),
	}
	go func() { _ = udpServer.ActivateAndServe() }()
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() {
		if err := udpServer.Shutdown(); err != nil {
			t.Errorf("Shutdown(UDP) error = %v", err)
		}
		if err := tcpServer.Shutdown(); err != nil {
			t.Errorf("Shutdown(TCP) error = %v", err)
		}
	})

	server := New(config.DNSConfig{Forwarders: []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}}, nil)
	request := new(dns.Msg)
	request.SetQuestion("unmapped.example.com.", dns.TypeA)
	writer := &recordingWriter{network: "udp"}

	server.ServeDNS(writer, request)

	if len(writer.message.Answer) != 1 {
		t.Fatalf("answer count = %d, want TCP response", len(writer.message.Answer))
	}
	if got := writer.message.Answer[0].(*dns.A).A.String(); got != "203.0.113.20" {
		t.Fatalf("answer = %s, want 203.0.113.20", got)
	}
}

func TestServeDNSReturnsServerFailureWhenForwardersFail(t *testing.T) {
	server := New(config.DNSConfig{Forwarders: []string{unusedUDPAddress(t)}}, nil)
	server.timeout = 20 * time.Millisecond
	request := new(dns.Msg)
	request.SetQuestion("unmapped.example.com.", dns.TypeA)
	writer := &recordingWriter{network: "udp"}

	server.ServeDNS(writer, request)

	if writer.message == nil || writer.message.Rcode != dns.RcodeServerFailure {
		t.Fatalf("response code = %v, want SERVFAIL", writer.message)
	}
}

func TestRunStopsAfterCancellation(t *testing.T) {
	server := New(config.DNSConfig{Listen: "127.0.0.1:0"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- server.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
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

type recordingWriter struct {
	message *dns.Msg
	network string
}

func (w *recordingWriter) LocalAddr() net.Addr {
	if w.network == "" {
		return testAddr("udp")
	}
	return testAddr(w.network)
}
func (w *recordingWriter) RemoteAddr() net.Addr             { return testAddr(w.network) }
func (w *recordingWriter) WriteMsg(message *dns.Msg) error  { w.message = message; return nil }
func (w *recordingWriter) Write([]byte) (int, error)        { return 0, nil }
func (w *recordingWriter) Close() error                     { return nil }
func (w *recordingWriter) TsigStatus() error                { return nil }
func (w *recordingWriter) TsigTimersOnly(bool)              {}
func (w *recordingWriter) Hijack()                          {}
func (w *recordingWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *recordingWriter) SetReadDeadline(time.Time) error  { return nil }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func startDNSServer(t *testing.T, network string, handler dns.HandlerFunc) (string, func()) {
	t.Helper()
	server := &dns.Server{Net: network, Handler: handler}
	var address net.Addr
	if network == "udp" {
		connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatalf("ListenUDP() error = %v", err)
		}
		server.PacketConn = connection
		address = connection.LocalAddr()
	} else {
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatalf("ListenTCP() error = %v", err)
		}
		server.Listener = listener
		address = listener.Addr()
	}
	go func() {
		_ = server.ActivateAndServe()
	}()
	return address.String(), func() {
		if err := server.Shutdown(); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}
}

func unusedUDPAddress(t *testing.T) string {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	port := connection.LocalAddr().(*net.UDPAddr).Port
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}
