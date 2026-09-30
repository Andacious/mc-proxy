package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
	"gopkg.in/yaml.v3"
)

const (
	defaultTTL         = 60
	defaultIdleTimeout = 2 * time.Minute
)

type Config struct {
	DNS      DNSConfig `yaml:"dns"`
	Mappings []Mapping `yaml:"mappings"`
}

type DNSConfig struct {
	Listen     string   `yaml:"listen"`
	Forwarders []string `yaml:"forwarders"`
	TTL        uint32   `yaml:"ttl"`
}

type Mapping struct {
	Domain      string        `yaml:"domain"`
	ProxyIP     net.IP        `yaml:"-"`
	ProxyIPText string        `yaml:"proxy_ip"`
	Listen      string        `yaml:"listen"`
	Target      string        `yaml:"target"`
	IdleTimeout time.Duration `yaml:"-"`
	IdleText    string        `yaml:"idle_timeout"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	return Decode(file)
}

func Decode(reader io.Reader) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode YAML: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg *Config) validate() error {
	if cfg.DNS.Listen == "" {
		return errors.New("dns.listen is required")
	}
	if err := validateAddress("dns.listen", cfg.DNS.Listen); err != nil {
		return err
	}
	if len(cfg.DNS.Forwarders) == 0 {
		return errors.New("at least one dns.forwarders entry is required")
	}
	for i, forwarder := range cfg.DNS.Forwarders {
		if err := validateAddress(fmt.Sprintf("dns.forwarders[%d]", i), forwarder); err != nil {
			return err
		}
	}
	if cfg.DNS.TTL == 0 {
		cfg.DNS.TTL = defaultTTL
	}
	if len(cfg.Mappings) == 0 {
		return errors.New("at least one mapping is required")
	}

	domains := make(map[string]struct{}, len(cfg.Mappings))
	listeners := make(map[string]struct{}, len(cfg.Mappings))
	proxyIPs := make(map[string]struct{}, len(cfg.Mappings))
	for i := range cfg.Mappings {
		mapping := &cfg.Mappings[i]
		prefix := fmt.Sprintf("mappings[%d]", i)

		mapping.Domain = canonicalDomain(mapping.Domain)
		if mapping.Domain == "." {
			return fmt.Errorf("%s.domain is required", prefix)
		}
		if _, ok := dns.IsDomainName(mapping.Domain); !ok {
			return fmt.Errorf("%s.domain %q is not a valid DNS name", prefix, mapping.Domain)
		}
		if _, exists := domains[mapping.Domain]; exists {
			return fmt.Errorf("%s.domain %q is duplicated", prefix, mapping.Domain)
		}
		domains[mapping.Domain] = struct{}{}

		mapping.ProxyIP = net.ParseIP(mapping.ProxyIPText)
		if mapping.ProxyIP == nil || mapping.ProxyIP.To4() == nil {
			return fmt.Errorf("%s.proxy_ip %q must be an IPv4 address", prefix, mapping.ProxyIPText)
		}
		mapping.ProxyIP = mapping.ProxyIP.To4()
		if _, exists := proxyIPs[mapping.ProxyIP.String()]; exists {
			return fmt.Errorf("%s.proxy_ip %q is duplicated", prefix, mapping.ProxyIPText)
		}
		proxyIPs[mapping.ProxyIP.String()] = struct{}{}

		if err := validateAddress(prefix+".listen", mapping.Listen); err != nil {
			return err
		}
		if _, exists := listeners[mapping.Listen]; exists {
			return fmt.Errorf("%s.listen %q is duplicated", prefix, mapping.Listen)
		}
		listeners[mapping.Listen] = struct{}{}

		if err := validateAddress(prefix+".target", mapping.Target); err != nil {
			return err
		}
		if mapping.IdleText == "" {
			mapping.IdleTimeout = defaultIdleTimeout
		} else {
			duration, err := time.ParseDuration(mapping.IdleText)
			if err != nil || duration <= 0 {
				return fmt.Errorf("%s.idle_timeout %q must be a positive duration", prefix, mapping.IdleText)
			}
			mapping.IdleTimeout = duration
		}
	}

	return nil
}

func validateAddress(field, address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s %q must include a host and port: %w", field, address, err)
	}
	if strings.TrimSpace(host) == "" && !strings.HasSuffix(field, ".listen") && field != "dns.listen" {
		return fmt.Errorf("%s %q must include a host", field, address)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("%s %q has an invalid port", field, address)
	}
	return nil
}

func canonicalDomain(domain string) string {
	return strings.ToLower(dns.Fqdn(strings.TrimSpace(domain)))
}
