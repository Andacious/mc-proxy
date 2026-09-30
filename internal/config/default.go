package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// bedrockPort is the UDP port Bedrock consoles use for featured servers.
const bedrockPort = "19132"

// featuredServers lists the Bedrock featured-server hostnames that are mapped
// to themselves by the default configuration.
var featuredServers = []string{
	"geo.hivebedrock.network",
	"mco.cubecraft.net",
	"mco.lbsg.net",
	"play.galaxite.net",
	"play.inpvp.net",
}

// Default returns the built-in configuration. Every current featured server is
// mapped to itself, so the proxy is a transparent pass-through until the
// targets are changed.
func Default() Config {
	cfg := Config{
		DNS: DNSConfig{
			Listen:     ":5353",
			Forwarders: []string{"1.1.1.1:53", "8.8.8.8:53"},
			TTL:        defaultTTL,
		},
	}

	for i, domain := range featuredServers {
		cfg.Mappings = append(cfg.Mappings, Mapping{
			Domain:      domain,
			ProxyIPText: fmt.Sprintf("192.168.1.%d", 241+i),
			Listen:      fmt.Sprintf(":%d", 19132+i),
			Target:      domain + ":" + bedrockPort,
			IdleText:    defaultIdleTimeout.String(),
		})
	}

	// Populate the derived fields and canonical domains.
	if err := cfg.Validate(); err != nil {
		panic("default configuration is invalid: " + err.Error())
	}
	return cfg
}

// LoadOrCreate reads the configuration at path. When the file does not exist
// it is created from Default() so that a fresh Docker volume is seeded with a
// working configuration.
func LoadOrCreate(path string) (Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, err
	}

	cfg = Default()
	if err := Save(path, cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Save writes cfg to path as YAML, replacing any existing file atomically.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create configuration directory %s: %w", dir, err)
	}

	temp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary configuration file: %w", err)
	}
	defer os.Remove(temp.Name())

	if err := Encode(temp, cfg); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary configuration file: %w", err)
	}
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return fmt.Errorf("set configuration permissions: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replace configuration file %s: %w", path, err)
	}
	return nil
}

// Encode writes cfg to writer as YAML.
func Encode(writer io.Writer, cfg Config) error {
	encoder := yaml.NewEncoder(writer)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	return encoder.Close()
}
