package webui

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Andacious/mc-proxy/internal/config"
)

// blankRows is the number of empty mapping rows offered for new entries.
const blankRows = 3

// maxFormBytes limits the size of a submitted configuration form.
const maxFormBytes = 1 << 20

// Server serves a small HTML form for editing the configuration file.
type Server struct {
	listen     string
	configPath string

	mu sync.Mutex
}

// New creates a configuration UI server that edits the file at configPath.
func New(listen, configPath string) *Server {
	return &Server{listen: listen, configPath: configPath}
}

// Run serves the UI until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		errs <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve configuration UI on %s: %w", s.listen, err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down configuration UI: %w", err)
	}
	return nil
}

// Handler returns the HTTP handler for the configuration UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

type pageData struct {
	DNSListen     string
	DNSForwarders string
	DNSTTL        string
	Mappings      []mappingRow
	Error         string
	Saved         bool
}

type mappingRow struct {
	Domain      string
	ProxyIP     string
	Listen      string
	Target      string
	IdleTimeout string
}

func (s *Server) handleIndex(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}

	switch request.Method {
	case http.MethodGet, http.MethodHead:
		s.mu.Lock()
		cfg, err := config.Load(s.configPath)
		s.mu.Unlock()
		if err != nil {
			http.Error(writer, "read configuration: "+err.Error(), http.StatusInternalServerError)
			return
		}
		render(writer, http.StatusOK, pageFromConfig(cfg))
	case http.MethodPost:
		s.handleSave(writer, request)
	default:
		writer.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSave(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxFormBytes)
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "read form: "+err.Error(), http.StatusBadRequest)
		return
	}

	page := pageFromForm(request.Form)
	cfg, err := configFromForm(request.Form)
	if err != nil {
		page.Error = err.Error()
		render(writer, http.StatusBadRequest, page)
		return
	}

	s.mu.Lock()
	err = config.Save(s.configPath, cfg)
	s.mu.Unlock()
	if err != nil {
		page.Error = err.Error()
		render(writer, http.StatusBadRequest, page)
		return
	}

	slog.Info("configuration saved from UI", "path", s.configPath, "mappings", len(cfg.Mappings))
	saved := pageFromConfig(cfg)
	saved.Saved = true
	render(writer, http.StatusOK, saved)
}

func pageFromConfig(cfg config.Config) pageData {
	page := pageData{
		DNSListen:     cfg.DNS.Listen,
		DNSForwarders: strings.Join(cfg.DNS.Forwarders, "\n"),
		DNSTTL:        strconv.FormatUint(uint64(cfg.DNS.TTL), 10),
	}
	for _, mapping := range cfg.Mappings {
		page.Mappings = append(page.Mappings, mappingRow{
			Domain:      strings.TrimSuffix(mapping.Domain, "."),
			ProxyIP:     mapping.ProxyIPText,
			Listen:      mapping.Listen,
			Target:      mapping.Target,
			IdleTimeout: mapping.IdleText,
		})
	}
	return withBlankRows(page)
}

func pageFromForm(form map[string][]string) pageData {
	page := pageData{
		DNSListen:     first(form, "dns_listen"),
		DNSForwarders: first(form, "dns_forwarders"),
		DNSTTL:        first(form, "dns_ttl"),
	}
	for _, row := range rows(form) {
		if row == (mappingRow{}) {
			continue
		}
		page.Mappings = append(page.Mappings, row)
	}
	return withBlankRows(page)
}

func withBlankRows(page pageData) pageData {
	for i := 0; i < blankRows; i++ {
		page.Mappings = append(page.Mappings, mappingRow{})
	}
	return page
}

func configFromForm(form map[string][]string) (config.Config, error) {
	cfg := config.Config{
		DNS: config.DNSConfig{
			Listen: first(form, "dns_listen"),
		},
	}

	for _, field := range strings.Fields(strings.ReplaceAll(first(form, "dns_forwarders"), ",", " ")) {
		cfg.DNS.Forwarders = append(cfg.DNS.Forwarders, field)
	}

	if text := first(form, "dns_ttl"); text != "" {
		ttl, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return config.Config{}, fmt.Errorf("dns.ttl %q must be a whole number of seconds", text)
		}
		cfg.DNS.TTL = uint32(ttl)
	}

	for _, row := range rows(form) {
		if row == (mappingRow{}) {
			continue
		}
		cfg.Mappings = append(cfg.Mappings, config.Mapping{
			Domain:      row.Domain,
			ProxyIPText: row.ProxyIP,
			Listen:      row.Listen,
			Target:      row.Target,
			IdleText:    row.IdleTimeout,
		})
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func rows(form map[string][]string) []mappingRow {
	count := 0
	for _, field := range []string{"domain", "proxy_ip", "listen", "target", "idle_timeout"} {
		if len(form[field]) > count {
			count = len(form[field])
		}
	}

	result := make([]mappingRow, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, mappingRow{
			Domain:      at(form, "domain", i),
			ProxyIP:     at(form, "proxy_ip", i),
			Listen:      at(form, "listen", i),
			Target:      at(form, "target", i),
			IdleTimeout: at(form, "idle_timeout", i),
		})
	}
	return result
}

func first(form map[string][]string, key string) string {
	return at(form, key, 0)
}

func at(form map[string][]string, key string, index int) string {
	values := form[key]
	if index >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[index])
}

func render(writer http.ResponseWriter, status int, page pageData) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(status)
	if err := pageTemplate.Execute(writer, page); err != nil {
		slog.Warn("render configuration UI", "error", err)
	}
}

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mc-proxy configuration</title>
<style>
body { font-family: system-ui, sans-serif; margin: 2rem; max-width: 60rem; }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #ccc; padding: 0.25rem; text-align: left; }
input, textarea { width: 100%; box-sizing: border-box; font: inherit; }
.notice { padding: 0.5rem; border-radius: 0.25rem; margin-bottom: 1rem; }
.saved { background: #e6ffed; border: 1px solid #34a853; }
.error { background: #ffecec; border: 1px solid #d93025; }
fieldset { margin-bottom: 1rem; }
</style>
</head>
<body>
<h1>mc-proxy configuration</h1>
{{if .Saved}}<p class="notice saved">Configuration saved. Restart the container to apply the changes.</p>{{end}}
{{if .Error}}<p class="notice error">{{.Error}}</p>{{end}}
<form method="post" action="/">
  <fieldset>
    <legend>DNS</legend>
    <p><label>Listen address<input name="dns_listen" value="{{.DNSListen}}"></label></p>
    <p><label>Forwarders (one per line)<textarea name="dns_forwarders" rows="3">{{.DNSForwarders}}</textarea></label></p>
    <p><label>TTL (seconds)<input name="dns_ttl" value="{{.DNSTTL}}"></label></p>
  </fieldset>
  <fieldset>
    <legend>Mappings</legend>
    <p>Clear every field in a row to remove that mapping.</p>
    <table>
      <tr><th>Domain</th><th>Proxy IP</th><th>Listen</th><th>Target</th><th>Idle timeout</th></tr>
      {{range .Mappings}}
      <tr>
        <td><input name="domain" value="{{.Domain}}"></td>
        <td><input name="proxy_ip" value="{{.ProxyIP}}"></td>
        <td><input name="listen" value="{{.Listen}}"></td>
        <td><input name="target" value="{{.Target}}"></td>
        <td><input name="idle_timeout" value="{{.IdleTimeout}}"></td>
      </tr>
      {{end}}
    </table>
  </fieldset>
  <button type="submit">Save</button>
</form>
</body>
</html>
`))
