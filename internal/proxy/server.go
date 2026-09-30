package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
)

const maxDatagramSize = 64 * 1024

type Set struct {
	proxies []*UDPProxy
}

type UDPProxy struct {
	domain      string
	target      string
	idleTimeout time.Duration
	listener    *net.UDPConn

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	client   *net.UDPAddr
	upstream *net.UDPConn
}

func NewAll(mappings []config.Mapping) (*Set, error) {
	set := &Set{}
	for _, mapping := range mappings {
		proxy, err := newUDPProxy(mapping)
		if err != nil {
			set.Close()
			return nil, err
		}
		set.proxies = append(set.proxies, proxy)
	}
	return set, nil
}

func newUDPProxy(mapping config.Mapping) (*UDPProxy, error) {
	address, err := net.ResolveUDPAddr("udp", mapping.Listen)
	if err != nil {
		return nil, fmt.Errorf("resolve listener for %s: %w", mapping.Domain, err)
	}
	listener, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for %s on %s: %w", mapping.Domain, mapping.Listen, err)
	}
	return &UDPProxy{
		domain:      mapping.Domain,
		target:      mapping.Target,
		idleTimeout: mapping.IdleTimeout,
		listener:    listener,
		sessions:    make(map[string]*session),
	}, nil
}

func (s *Set) Run(ctx context.Context) error {
	errs := make(chan error, len(s.proxies))
	for _, current := range s.proxies {
		proxy := current
		go func() {
			errs <- proxy.run(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		s.Close()
		return nil
	case err := <-errs:
		s.Close()
		return err
	}
}

func (s *Set) Close() {
	for _, proxy := range s.proxies {
		proxy.close()
	}
}

func (p *UDPProxy) run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		p.close()
	}()

	slog.Info("UDP proxy listening",
		"domain", p.domain,
		"listen", p.listener.LocalAddr(),
		"target", p.target,
	)

	buffer := make([]byte, maxDatagramSize)
	for {
		size, client, err := p.listener.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read client packet for %s: %w", p.domain, err)
		}

		current, err := p.getSession(client)
		if err != nil {
			slog.Error("open upstream session",
				"domain", p.domain,
				"client", client,
				"target", p.target,
				"error", err,
			)
			continue
		}
		if err := current.upstream.SetReadDeadline(time.Now().Add(p.idleTimeout)); err != nil {
			p.removeSession(client.String(), current)
			continue
		}
		if _, err := current.upstream.Write(buffer[:size]); err != nil {
			slog.Warn("forward client packet", "domain", p.domain, "client", client, "error", err)
			p.removeSession(client.String(), current)
		}
	}
}

func (p *UDPProxy) getSession(client *net.UDPAddr) (*session, error) {
	key := client.String()
	p.mu.Lock()
	defer p.mu.Unlock()

	if current, ok := p.sessions[key]; ok {
		return current, nil
	}
	target, err := net.ResolveUDPAddr("udp", p.target)
	if err != nil {
		return nil, fmt.Errorf("resolve target %s: %w", p.target, err)
	}
	upstream, err := net.DialUDP("udp", nil, target)
	if err != nil {
		return nil, fmt.Errorf("connect to target %s: %w", p.target, err)
	}
	current := &session{client: client, upstream: upstream}
	p.sessions[key] = current
	go p.copyResponses(key, current)
	return current, nil
}

func (p *UDPProxy) copyResponses(key string, current *session) {
	buffer := make([]byte, maxDatagramSize)
	for {
		size, err := current.upstream.Read(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
					slog.Warn("read upstream packet", "domain", p.domain, "client", current.client, "error", err)
				}
			}
			p.removeSession(key, current)
			return
		}
		if _, err := p.listener.WriteToUDP(buffer[:size], current.client); err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("forward upstream packet", "domain", p.domain, "client", current.client, "error", err)
			}
			p.removeSession(key, current)
			return
		}
	}
}

func (p *UDPProxy) removeSession(key string, current *session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions[key] != current {
		return
	}
	delete(p.sessions, key)
	_ = current.upstream.Close()
}

func (p *UDPProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, current := range p.sessions {
		_ = current.upstream.Close()
		delete(p.sessions, key)
	}
}
