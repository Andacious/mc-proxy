package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Andacious/mc-proxy/internal/config"
	"github.com/Andacious/mc-proxy/internal/dnsserver"
	"github.com/Andacious/mc-proxy/internal/proxy"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/mc-proxy/config.yaml", "path to the YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dns := dnsserver.New(cfg.DNS, cfg.Mappings)
	proxies, err := proxy.NewAll(cfg.Mappings)
	if err != nil {
		return err
	}
	defer proxies.Close()

	errs := make(chan error, 2)
	go func() {
		errs <- dns.Run(ctx)
	}()
	go func() {
		errs <- proxies.Run(ctx)
	}()

	slog.Info("mc-proxy started",
		"dns_listen", cfg.DNS.Listen,
		"mappings", len(cfg.Mappings),
	)

	select {
	case <-ctx.Done():
		return nil
	case err := <-errs:
		stop()
		if err == nil {
			return nil
		}
		return err
	}
}
