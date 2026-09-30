package dnsserver

import (
	"net"
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

type recordingWriter struct {
	message *dns.Msg
}

func (w *recordingWriter) LocalAddr() net.Addr              { return testAddr("udp") }
func (w *recordingWriter) RemoteAddr() net.Addr             { return testAddr("udp") }
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
