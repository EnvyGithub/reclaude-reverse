package gateway

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// Config is the Gateway configuration.
type Config struct {
	Listen  string         `json:"listen"`
	Tokens  []TokenAccount `json:"tokens"`
	SKFile  string         `json:"sk_file"`
}

// LoadConfig reads gateway config from a JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// Server is the Gateway HTTP server.
type Server struct {
	pool    *TokenPool
	skStore *SKStore
	client  *http.Client
}

// NewServer creates a new Gateway server.
func NewServer(pool *TokenPool, skStore *SKStore) *Server {
	return &Server{
		pool:    pool,
		skStore: skStore,
		client:  chromeHTTPClient(),
	}
}

// ServeHTTP handles incoming requests from MITM proxies.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Authenticate SK
	sk := r.Header.Get("X-SK-Token")
	if sk == "" {
		log.Printf("[GW] ✗ Request without SK from %s", r.RemoteAddr)
		http.Error(w, `{"error":"missing X-SK-Token"}`, http.StatusUnauthorized)
		return
	}

	skKey, err := s.skStore.Verify(sk)
	if err != nil {
		log.Printf("[GW] ✗ SK auth failed: %v (from %s)", err, r.RemoteAddr)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusForbidden)
		return
	}

	// 2. Pick a token from the pool
	accountID, oauthToken, err := s.pool.Pick()
	if err != nil {
		log.Printf("[GW] ✗ No available accounts: %v", err)
		http.Error(w, `{"error":"no available accounts, try later"}`, http.StatusTooManyRequests)
		return
	}

	// 3. Determine target host
	originalHost := r.Header.Get("X-Original-Host")
	if originalHost == "" {
		originalHost = "api.anthropic.com"
	}

	log.Printf("[GW] ✓ SK=%s user=%s → account=%s → %s%s",
		sk[:12]+"...", skKey.Name, accountID, originalHost, r.URL.Path)

	// 4. Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}

	// 5. Build forwarding request
	targetURL := "https://" + originalHost + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	fwdReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, "create request error", http.StatusInternalServerError)
		return
	}

	// 6. Copy headers (skip proxy/gateway headers)
	for k, vv := range r.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-original-") ||
			strings.HasPrefix(lower, "x-forwarded-") ||
			strings.HasPrefix(lower, "x-sk-") {
			continue
		}
		for _, v := range vv {
			fwdReq.Header.Add(k, v)
		}
	}

	// 7. Inject real OAuth token (only for api.anthropic.com)
	if originalHost == "api.anthropic.com" {
		fwdReq.Header.Set("Authorization", "Bearer "+oauthToken)
		if fwdReq.Header.Get("anthropic-version") == "" {
			fwdReq.Header.Set("anthropic-version", "2023-06-01")
		}
	}
	fwdReq.Host = originalHost

	// 8. Forward with utls Chrome fingerprint
	resp, err := s.client.Do(fwdReq)
	if err != nil {
		log.Printf("[GW] ✗ Forward error: %v", err)
		http.Error(w, fmt.Sprintf("forward error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Record usage
	s.skStore.RecordUsage(sk)

	log.Printf("[GW] → %s %d %s", accountID, resp.StatusCode, http.StatusText(resp.StatusCode))

	// 9. Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 10. Stream response body
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32768)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

// HandleStats returns pool and SK statistics as JSON.
func (s *Server) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"accounts": s.pool.Stats(),
		"sk_keys":  s.skStore.List(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// chromeHTTPClient creates an HTTP client with Chrome TLS fingerprint via utls.
func chromeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialTLS: func(network, addr string) (net.Conn, error) {
				conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
				if err != nil {
					return nil, err
				}

				host, _, _ := net.SplitHostPort(addr)

				spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
				if err != nil {
					conn.Close()
					return nil, fmt.Errorf("utls spec: %v", err)
				}

				for i, ext := range spec.Extensions {
					if alpn, ok := ext.(*utls.ALPNExtension); ok {
						alpn.AlpnProtocols = []string{"http/1.1"}
						spec.Extensions[i] = alpn
					}
				}

				uconn := utls.UClient(conn, &utls.Config{
					ServerName: host,
				}, utls.HelloCustom)

				if err := uconn.ApplyPreset(&spec); err != nil {
					conn.Close()
					return nil, fmt.Errorf("utls preset: %v", err)
				}

				if err := uconn.Handshake(); err != nil {
					conn.Close()
					return nil, fmt.Errorf("utls handshake: %v", err)
				}

				return uconn, nil
			},
			TLSNextProto:       make(map[string]func(string, *tls.Conn) http.RoundTripper),
			DisableCompression: true,
		},
	}
}
