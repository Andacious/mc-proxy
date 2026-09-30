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
	"github.com/Andacious/mc-proxy/internal/webui"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/mc-proxy/config.yaml", "path to the YAML configuration file")
	uiListen := flag.String("ui-listen", ":8080", "listen address for the configuration UI, or empty to disable it")
	flag.Parse()

	cfg, created, err := config.LoadOrCreate(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if created {
		slog.Info("created default configuration", "path", *configPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dns := dnsserver.New(cfg.DNS, cfg.Mappings)
	proxies, err := proxy.NewAll(cfg.Mappings)
	if err != nil {
		return err
	}
	defer proxies.Close()

	errs := make(chan error, 3)
	go func() {
		errs <- dns.Run(ctx)
	}()
	go func() {
		errs <- proxies.Run(ctx)
	}()
	uiStatus := "disabled"
	if *uiListen != "" {
		uiStatus = *uiListen
		ui := webui.New(*uiListen, *configPath)
		go func() {
			errs <- ui.Run(ctx)
		}()
	}

	slog.Info("mc-proxy started",
		"dns_listen", cfg.DNS.Listen,
		"mappings", len(cfg.Mappings),
		"ui_listen", uiStatus,
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
