package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validConfig = `
dns:
  listen: ":5353"
  forwarders:
    - "1.1.1.1:53"
mappings:
  - domain: "Featured.Example.COM"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "home.example.net:20001"
`

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

func TestDecodePreservesExplicitValues(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
dns:
  listen: "127.0.0.1:5353"
  forwarders: ["1.1.1.1:53"]
  ttl: 300
mappings:
  - domain: "featured.example.com"
    proxy_ip: "192.168.1.241"
    listen: "127.0.0.1:19132"
    target: "home.example.net:20001"
    idle_timeout: "30s"
`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.DNS.TTL != 300 {
		t.Fatalf("TTL = %d, want 300", cfg.DNS.TTL)
	}
	if got := cfg.Mappings[0].IdleTimeout; got != 30*time.Second {
		t.Fatalf("IdleTimeout = %s, want 30s", got)
	}
}

func TestLoadReadsConfigurationFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Mappings) != 1 {
		t.Fatalf("mapping count = %d, want 1", len(cfg.Mappings))
	}
}

func TestLoadReturnsOpenError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want open error")
	}
}

func TestDecodeRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{"invalid YAML", "dns: [", "decode YAML"},
		{"unknown field", validConfig + "\nunknown: true\n", "field unknown"},
		{"missing DNS listener", strings.Replace(validConfig, `listen: ":5353"`, `listen: ""`, 1), "dns.listen is required"},
		{"invalid DNS listener", strings.Replace(validConfig, `listen: ":5353"`, `listen: "5353"`, 1), "dns.listen"},
		{"missing forwarders", strings.Replace(validConfig, "  forwarders:\n    - \"1.1.1.1:53\"\n", "", 1), "dns.forwarders"},
		{"invalid forwarder", strings.Replace(validConfig, `"1.1.1.1:53"`, `"1.1.1.1"`, 1), "dns.forwarders[0]"},
		{"missing mappings", strings.Split(validConfig, "mappings:")[0], "at least one mapping"},
		{"missing domain", strings.Replace(validConfig, `"Featured.Example.COM"`, `""`, 1), "domain is required"},
		{"invalid domain", strings.Replace(validConfig, `"Featured.Example.COM"`, `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com"`, 1), "not a valid DNS name"},
		{"invalid proxy IP", strings.Replace(validConfig, `"192.168.1.241"`, `"2001:db8::1"`, 1), "must be an IPv4 address"},
		{"invalid listener", strings.Replace(validConfig, `listen: ":19132"`, `listen: "19132"`, 1), "mappings[0].listen"},
		{"missing target host", strings.Replace(validConfig, `"home.example.net:20001"`, `":20001"`, 1), "must include a host"},
		{"invalid target port", strings.Replace(validConfig, `"home.example.net:20001"`, `"home.example.net:0"`, 1), "invalid port"},
		{"invalid idle timeout", validConfig + "    idle_timeout: \"never\"\n", "positive duration"},
		{"zero idle timeout", validConfig + "    idle_timeout: \"0s\"\n", "positive duration"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Decode() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestDecodeRejectsDuplicateMappingValues(t *testing.T) {
	secondMapping := `
  - domain: "two.example.com"
    proxy_ip: "192.168.1.242"
    listen: ":19133"
    target: "home.example.net:20002"
`
	tests := []struct {
		name      string
		duplicate string
		wantError string
	}{
		{"domain", `"Featured.Example.COM"`, "domain"},
		{"proxy IP", `"192.168.1.241"`, "proxy_ip"},
		{"listener", `":19132"`, "listen"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validConfig + strings.Replace(secondMapping, map[string]string{
				"domain":   `"two.example.com"`,
				"proxy IP": `"192.168.1.242"`,
				"listener": `":19133"`,
			}[test.name], test.duplicate, 1)
			_, err := Decode(strings.NewReader(input))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Decode() error = %v, want duplicate %s error", err, test.name)
			}
		})
	}
}
