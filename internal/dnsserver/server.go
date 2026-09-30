package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
	"github.com/miekg/dns"
)

type Server struct {
	listen     string
	ttl        uint32
	forwarders []string
	answers    map[string]net.IP
	timeout    time.Duration
	next       atomic.Uint64
}

func New(cfg config.DNSConfig, mappings []config.Mapping) *Server {
	answers := make(map[string]net.IP, len(mappings))
	for _, mapping := range mappings {
		answers[mapping.Domain] = mapping.ProxyIP
	}
	return &Server{
		listen:     cfg.Listen,
		ttl:        cfg.TTL,
		forwarders: cfg.Forwarders,
		answers:    answers,
		timeout:    5 * time.Second,
	}
}

func (s *Server) Run(ctx context.Context) error {
	udp := &dns.Server{Addr: s.listen, Net: "udp", Handler: s}
	tcp := &dns.Server{Addr: s.listen, Net: "tcp", Handler: s}
	errs := make(chan error, 2)

	go func() {
		errs <- udp.ListenAndServe()
	}()
	go func() {
		errs <- tcp.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		return fmt.Errorf("serve DNS on %s: %w", s.listen, err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var shutdownErr error
	if err := udp.ShutdownContext(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shut down UDP DNS server: %w", err))
	}
	if err := tcp.ShutdownContext(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shut down TCP DNS server: %w", err))
	}
	return shutdownErr
}

func (s *Server) ServeDNS(writer dns.ResponseWriter, request *dns.Msg) {
	if len(request.Question) == 1 {
		question := request.Question[0]
		if answer, ok := s.answers[strings.ToLower(question.Name)]; ok {
			response := new(dns.Msg)
			response.SetReply(request)
			response.Authoritative = true
			response.RecursionAvailable = true
			if question.Qtype == dns.TypeA || question.Qtype == dns.TypeANY {
				response.Answer = append(response.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   question.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    s.ttl,
					},
					A: answer,
				})
			}
			if err := writer.WriteMsg(response); err != nil {
				slog.Warn("write mapped DNS response", "name", question.Name, "error", err)
			}
			return
		}
	}

	s.forward(writer, request)
}

func (s *Server) forward(writer dns.ResponseWriter, request *dns.Msg) {
	start := s.next.Add(1)
	network := "udp"
	if strings.HasPrefix(writer.LocalAddr().Network(), "tcp") {
		network = "tcp"
	}

	var lastErr error
	for i := range s.forwarders {
		forwarder := s.forwarders[(int(start)+i)%len(s.forwarders)]
		client := &dns.Client{Net: network, Timeout: s.timeout}
		response, _, err := client.Exchange(request, forwarder)
		if err != nil {
			lastErr = err
			continue
		}
		if response.Truncated && network == "udp" {
			client.Net = "tcp"
			response, _, err = client.Exchange(request, forwarder)
			if err != nil {
				lastErr = err
				continue
			}
		}
		if err := writer.WriteMsg(response); err != nil {
			slog.Warn("write forwarded DNS response", "error", err)
		}
		return
	}

	slog.Error("all DNS forwarders failed", "error", lastErr)
	response := new(dns.Msg)
	response.SetRcode(request, dns.RcodeServerFailure)
	if err := writer.WriteMsg(response); err != nil {
		slog.Warn("write DNS failure response", "error", err)
	}
}
