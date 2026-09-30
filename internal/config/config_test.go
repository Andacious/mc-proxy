package config

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeAppliesDefaultsAndCanonicalizesDomains(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
dns:
  listen: ":5353"
  forwarders:
    - "1.1.1.1:53"
mappings:
  - domain: "Featured.Example.COM"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "home.example.net:20001"
`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if cfg.DNS.TTL != 60 {
		t.Fatalf("TTL = %d, want 60", cfg.DNS.TTL)
	}
	mapping := cfg.Mappings[0]
	if mapping.Domain != "featured.example.com." {
		t.Fatalf("Domain = %q, want canonical domain", mapping.Domain)
	}
	if mapping.IdleTimeout != 2*time.Minute {
		t.Fatalf("IdleTimeout = %s, want 2m", mapping.IdleTimeout)
	}
}

func TestDecodeRejectsDuplicateProxyIPs(t *testing.T) {
	_, err := Decode(strings.NewReader(`
dns:
  listen: ":5353"
  forwarders: ["1.1.1.1:53"]
mappings:
  - domain: "one.example.com"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "home.example.net:20001"
  - domain: "two.example.com"
    proxy_ip: "192.168.1.241"
    listen: ":19133"
    target: "home.example.net:20002"
`))
	if err == nil || !strings.Contains(err.Error(), "proxy_ip") {
		t.Fatalf("Decode() error = %v, want duplicate proxy_ip error", err)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	_, err := Decode(strings.NewReader(`
dns:
  listen: ":5353"
  forwarders: ["1.1.1.1:53"]
unknown: true
mappings:
  - domain: "one.example.com"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "home.example.net:20001"
`))
	if err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("Decode() error = %v, want unknown field error", err)
	}
}
