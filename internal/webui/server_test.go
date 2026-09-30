package webui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return New("127.0.0.1:0", path), path
}

func TestIndexRendersCurrentConfiguration(t *testing.T) {
	server, _ := newTestServer(t)
	recorder := httptest.NewRecorder()

	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{"geo.hivebedrock.network", "1.1.1.1:53", ":5353"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestIndexReportsMissingConfiguration(t *testing.T) {
	server := New("127.0.0.1:0", filepath.Join(t.TempDir(), "missing.yaml"))
	recorder := httptest.NewRecorder()

	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestUnknownPathAndMethod(t *testing.T) {
	server, _ := newTestServer(t)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/other", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func postForm(t *testing.T, server *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestSaveWritesSubmittedConfiguration(t *testing.T) {
	server, path := newTestServer(t)

	form := url.Values{
		"dns_listen":     {":5353"},
		"dns_forwarders": {"9.9.9.9:53\n1.1.1.1:53"},
		"dns_ttl":        {"120"},
		"domain":         {"geo.hivebedrock.network", "", ""},
		"proxy_ip":       {"192.168.1.241", "", ""},
		"listen":         {":19132", "", ""},
		"target":         {"home.example.net:20001", "", ""},
		"idle_timeout":   {"3m", "", ""},
	}

	recorder := postForm(t, server, form)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "Configuration saved") {
		t.Fatalf("body missing the saved notice: %s", recorder.Body)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Mappings) != 1 {
		t.Fatalf("mappings = %d, want 1", len(cfg.Mappings))
	}
	if cfg.Mappings[0].Target != "home.example.net:20001" {
		t.Fatalf("target = %q, want %q", cfg.Mappings[0].Target, "home.example.net:20001")
	}
	if cfg.Mappings[0].IdleTimeout != 3*time.Minute {
		t.Fatalf("idle timeout = %s, want 3m", cfg.Mappings[0].IdleTimeout)
	}
	if cfg.DNS.TTL != 120 {
		t.Fatalf("ttl = %d, want 120", cfg.DNS.TTL)
	}
	if len(cfg.DNS.Forwarders) != 2 {
		t.Fatalf("forwarders = %v, want 2 entries", cfg.DNS.Forwarders)
	}
}

func TestSaveRejectsInvalidInput(t *testing.T) {
	server, path := newTestServer(t)

	cases := map[string]url.Values{
		"invalid ttl": {
			"dns_listen":     {":5353"},
			"dns_forwarders": {"1.1.1.1:53"},
			"dns_ttl":        {"soon"},
			"domain":         {"one.example.net"},
			"proxy_ip":       {"192.168.1.241"},
			"listen":         {":19132"},
			"target":         {"home.example.net:20001"},
		},
		"invalid proxy ip": {
			"dns_listen":     {":5353"},
			"dns_forwarders": {"1.1.1.1:53"},
			"domain":         {"one.example.net"},
			"proxy_ip":       {"not-an-ip"},
			"listen":         {":19132"},
			"target":         {"home.example.net:20001"},
		},
		"no mappings": {
			"dns_listen":     {":5353"},
			"dns_forwarders": {"1.1.1.1:53"},
		},
	}

	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := postForm(t, server, form)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}

			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(cfg.Mappings) != 5 {
				t.Fatalf("mappings = %d, want the stored defaults to be unchanged", len(cfg.Mappings))
			}
		})
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	_, path := newTestServer(t)
	server := New("127.0.0.1:0", path)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx)
	}()

	var lastErr error
	for i := 0; i < 50; i++ {
		address := server.Addr()
		if address == "" {
			lastErr = errors.New("listener not ready")
			time.Sleep(20 * time.Millisecond)
			continue
		}
		response, err := http.Get("http://" + address + "/")
		if err == nil {
			response.Body.Close()
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		cancel()
		<-done
		t.Fatalf("Get() error = %v", lastErr)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunReportsListenFailure(t *testing.T) {
	_, path := newTestServer(t)
	server := New("256.256.256.256:1", path)

	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want a listen error")
	}
}

func TestSaveRejectsCrossOriginSubmissions(t *testing.T) {
	server, _ := newTestServer(t)
	form := url.Values{
		"dns_listen":     {":5353"},
		"dns_forwarders": {"1.1.1.1:53"},
		"domain":         {"one.example.net"},
		"proxy_ip":       {"192.168.1.241"},
		"listen":         {":19132"},
		"target":         {"home.example.net:20001"},
	}

	cases := map[string]map[string]string{
		"cross-site fetch metadata": {"Sec-Fetch-Site": "cross-site"},
		"foreign origin":            {"Origin": "http://evil.example.com"},
	}

	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for key, value := range headers {
				request.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
			}
		})
	}
}

func TestSaveAllowsSameOriginSubmission(t *testing.T) {
	server, _ := newTestServer(t)
	form := url.Values{
		"dns_listen":     {":5353"},
		"dns_forwarders": {"1.1.1.1:53"},
		"domain":         {"one.example.net"},
		"proxy_ip":       {"192.168.1.241"},
		"listen":         {":19132"},
		"target":         {"home.example.net:20001"},
	}

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
}
