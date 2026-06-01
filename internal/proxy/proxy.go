// Package proxy implements the local MITM proxy server.
// It intercepts HTTPS traffic from Claude Code/Desktop App directed at
// api.anthropic.com and forwards it to a local backend server.
//
// Two modes for intercepted domains:
//  - API calls to /v1/messages → forward to local backend (intercept)
//  - Everything else → forward to real upstream server (transparent)
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"my-claude-proxy/internal/ca"
)

// Server is the MITM proxy server.
type Server struct {
	backendAddr string
	leafCache   *ca.LeafCache
	caRoot      *ca.Root
}

// NewServer creates a new MITM proxy server.
func NewServer(caRoot *ca.Root, backendAddr string) *Server {
	return &Server{
		backendAddr: backendAddr,
		leafCache:   ca.NewLeafCache(caRoot),
		caRoot:      caRoot,
	}
}

// ListenAndServe starts the proxy server on the given address.
func (s *Server) ListenAndServe(ctx context.Context, listenAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	defer ln.Close()

	log.Printf("[PROXY] MITM proxy listening on %s", listenAddr)
	log.Printf("[PROXY] Forwarding intercepted API calls to backend: %s", s.backendAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				log.Printf("[PROXY] Accept error: %v", err)
				continue
			}
		}
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection processes a single client connection.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		s.handleConnect(ctx, conn, br, req)
	} else {
		s.handlePlainHTTP(ctx, conn, req)
	}
}

// handleConnect handles HTTP CONNECT tunneling (used for HTTPS).
func (s *Server) handleConnect(ctx context.Context, conn net.Conn, br *bufio.Reader, req *http.Request) {
	host := hostOnly(req.Host)

	if shouldIntercept(host) {
		log.Printf("[PROXY] ✓ INTERCEPTING: %s", req.Host)

		_, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		if err != nil {
			return
		}

		s.interceptTLS(ctx, conn, host)
	} else {
		log.Printf("[PROXY] → PASSTHROUGH: %s", req.Host)

		upstream, err := net.DialTimeout("tcp", req.Host, 10*time.Second)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer upstream.Close()

		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		bidiSplice(conn, upstream)
	}
}

// interceptTLS performs MITM on a TLS connection.
// For each request within the TLS session, it decides:
//   - If it's an API call to intercept (e.g. /v1/messages) → forward to local backend
//   - Otherwise → forward transparently to the real upstream server
func (s *Server) interceptTLS(ctx context.Context, rawConn net.Conn, hostname string) {
	leafCert, err := s.leafCache.GetTLSCert(hostname)
	if err != nil {
		log.Printf("[PROXY] Failed to generate leaf cert for %s: %v", hostname, err)
		return
	}

	tlsConn := tls.Server(rawConn, &tls.Config{
		Certificates: []tls.Certificate{*leafCert},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		log.Printf("[PROXY] TLS handshake failed for %s: %v", hostname, err)
		return
	}
	defer tlsConn.Close()

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				log.Printf("[PROXY] Read error from %s: %v", hostname, err)
			}
			return
		}

		req.URL.Scheme = "https"
		req.URL.Host = hostname
		req.RequestURI = ""

		if shouldMockResponse(hostname, req.URL.Path, req.Method) {
			// ═══════════════════════════════════════════
			// MOCK: Return fake response for auth/validation endpoints
			// ═══════════════════════════════════════════
			log.Printf("[PROXY] 🔑 MOCK %s %s://%s%s", req.Method, "https", hostname, req.URL.Path)
			mockResp := buildMockResponse(hostname, req.URL.Path, req.Method)
			if _, err := tlsConn.Write([]byte(mockResp)); err != nil {
				log.Printf("[PROXY] Mock write error: %v", err)
				return
			}
			// Drain request body if any
			if req.Body != nil {
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
			}
		} else if shouldInterceptRequest(hostname, req.URL.Path) {
			// ═══════════════════════════════════════════
			// INTERCEPT: Forward to local backend for processing
			// ═══════════════════════════════════════════
			log.Printf("[PROXY] 🎯 INTERCEPT %s %s://%s%s → backend", req.Method, "https", hostname, req.URL.Path)

			resp, err := s.forwardToBackend(ctx, req, hostname)
			if err != nil {
				log.Printf("[PROXY] Backend error: %v", err)
				errResp := fmt.Sprintf("HTTP/1.1 502 Bad Gateway\r\nContent-Length: %d\r\n\r\n%s",
					len(err.Error()), err.Error())
				tlsConn.Write([]byte(errResp))
				return
			}
			if err := s.writeResponse(tlsConn, resp); err != nil {
				log.Printf("[PROXY] Response write error: %v", err)
				resp.Body.Close()
				return
			}
			resp.Body.Close()
		} else {
			// ═══════════════════════════════════════════
			// TRANSPARENT: Forward to real upstream server
			// ═══════════════════════════════════════════
			log.Printf("[PROXY] ↗ FORWARD %s %s://%s%s → upstream", req.Method, "https", hostname, req.URL.Path)

			resp, err := s.forwardToUpstream(ctx, req, hostname)
			if err != nil {
				log.Printf("[PROXY] Upstream error for %s%s: %v", hostname, req.URL.Path, err)
				errResp := fmt.Sprintf("HTTP/1.1 502 Bad Gateway\r\nContent-Length: %d\r\n\r\n%s",
					len(err.Error()), err.Error())
				tlsConn.Write([]byte(errResp))
				return
			}
			if err := s.writeResponse(tlsConn, resp); err != nil {
				log.Printf("[PROXY] Response write error: %v", err)
				resp.Body.Close()
				return
			}
			resp.Body.Close()
		}
	}
}

// forwardToUpstream sends the request to the REAL upstream server (transparent forwarding).
func (s *Server) forwardToUpstream(ctx context.Context, origReq *http.Request, hostname string) (*http.Response, error) {
	// Build the real upstream URL
	upstreamURL := fmt.Sprintf("https://%s%s", hostname, origReq.URL.Path)
	if origReq.URL.RawQuery != "" {
		upstreamURL += "?" + origReq.URL.RawQuery
	}

	fwdReq, err := http.NewRequestWithContext(ctx, origReq.Method, upstreamURL, origReq.Body)
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}

	// Copy all headers exactly as they are
	for k, vv := range origReq.Header {
		for _, v := range vv {
			fwdReq.Header.Add(k, v)
		}
	}
	fwdReq.Host = hostname

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil, // Don't use system proxy (avoid loop!)
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
			MaxResponseHeaderBytes: 0,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Minute,
	}

	return client.Do(fwdReq)
}

// forwardToBackend sends the intercepted request to the local backend server.
func (s *Server) forwardToBackend(ctx context.Context, origReq *http.Request, originalHost string) (*http.Response, error) {
	backendURL := fmt.Sprintf("http://%s%s", s.backendAddr, origReq.URL.Path)
	if origReq.URL.RawQuery != "" {
		backendURL += "?" + origReq.URL.RawQuery
	}

	fwdReq, err := http.NewRequestWithContext(ctx, origReq.Method, backendURL, origReq.Body)
	if err != nil {
		return nil, fmt.Errorf("create forward request: %w", err)
	}

	for k, vv := range origReq.Header {
		for _, v := range vv {
			fwdReq.Header.Add(k, v)
		}
	}

	fwdReq.Header.Set("X-Original-Host", originalHost)
	fwdReq.Header.Set("X-Forwarded-Proto", "https")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
			MaxResponseHeaderBytes: 0,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Minute,
	}

	return client.Do(fwdReq)
}

// writeResponse writes an HTTP response back to the client over TLS.
func (s *Server) writeResponse(w io.Writer, resp *http.Response) error {
	statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	if _, err := w.Write([]byte(statusLine)); err != nil {
		return err
	}

	isStreaming := false
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		isStreaming = true
	}
	transferEncoding := resp.Header.Get("Transfer-Encoding")
	if strings.Contains(transferEncoding, "chunked") {
		isStreaming = true
	}

	stripHopByHop(resp.Header)

	if isStreaming {
		resp.Header.Del("Content-Length")
		resp.Header.Set("Transfer-Encoding", "chunked")
	}

	if err := resp.Header.Write(w); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\r\n")); err != nil {
		return err
	}

	if resp.Body == nil {
		return nil
	}

	if isStreaming {
		return s.streamChunked(w, resp.Body)
	}

	_, err := io.Copy(w, resp.Body)
	return err
}

// streamChunked writes the response body using HTTP chunked transfer encoding.
func (s *Server) streamChunked(w io.Writer, body io.Reader) error {
	buf := make([]byte, 8192)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			chunkHeader := fmt.Sprintf("%x\r\n", n)
			if _, werr := w.Write([]byte(chunkHeader)); werr != nil {
				return werr
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if _, werr := w.Write([]byte("\r\n")); werr != nil {
				return werr
			}
			if f, ok := w.(interface{ Flush() error }); ok {
				f.Flush()
			}
		}
		if err == io.EOF {
			w.Write([]byte("0\r\n\r\n"))
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// handlePlainHTTP proxies a plain HTTP request (non-CONNECT).
func (s *Server) handlePlainHTTP(ctx context.Context, conn net.Conn, req *http.Request) {
	log.Printf("[PROXY] Plain HTTP: %s %s", req.Method, req.URL.String())
	conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\nOnly HTTPS CONNECT is supported\n"))
}

// shouldIntercept checks if the given hostname should be MITM intercepted
// (i.e., TLS terminated and re-encrypted). All traffic for these hosts
// passes through us; we can then decide per-request whether to forward
// to the backend or to the real upstream.
func shouldIntercept(host string) bool {
	host = hostOnly(host)
	switch host {
	case "api.anthropic.com",
		"statsig.anthropic.com":
		return true
	}
	if strings.HasSuffix(host, ".anthropic.com") {
		return true
	}
	// claude.ai uses passthrough — Cloudflare JS Challenge blocks MITM even with utls.
	// API endpoints (healthcheck) pass, but HTML pages get 403.
	return false
}

// shouldInterceptRequest decides whether a specific request should be
// forwarded to the local backend (true) or to the real upstream (false).
func shouldInterceptRequest(host, path string) bool {
	host = hostOnly(host)

	switch host {
	case "api.anthropic.com":
		// Conversation API → backend
		if strings.HasPrefix(path, "/v1/messages") {
			return true
		}
		if strings.HasPrefix(path, "/v1/complete") {
			return true
		}
		return false

	case "claude.ai":
		// ALL claude.ai requests go through backend (utls Chrome fingerprint)
		// This bypasses Cloudflare's TLS fingerprint detection
		return true
	}
	return false
}

// shouldMockResponse checks if a request should be answered with a mock
// response directly by the proxy (auth/validation endpoints).
func shouldMockResponse(host, path, method string) bool {
	host = hostOnly(host)
	if host == "api.anthropic.com" {
		switch {
		case path == "/api/hello":
			return true
		case strings.HasPrefix(path, "/mcp-registry"):
			return true
		case strings.HasPrefix(path, "/api/event_logging"):
			return true
		case path == "/api/auth/me":
			return true
		case strings.HasPrefix(path, "/api/claude_code/settings"):
			return true
		case strings.HasPrefix(path, "/api/claude_code/policy_limits"):
			return true
		case strings.HasPrefix(path, "/api/claude_cli/bootstrap"):
			return true
		case strings.HasPrefix(path, "/api/eval/"):
			return true
		case strings.HasPrefix(path, "/api/claude_code_penguin_mode"):
			return true
		}
	}
	if host == "statsig.anthropic.com" {
		return true
	}
	return false
}

// buildMockResponse creates a fake HTTP response for auth/validation endpoints.
func buildMockResponse(host, path, method string) string {
	host = hostOnly(host)

	if host == "api.anthropic.com" {
		switch {
		case path == "/api/hello":
			return mockHTTPResponse(200, "application/json", `{
				"account_uuid": "fake-uuid-0000-0000-000000000001",
				"email_address": "user@example.com",
				"display_name": "Claude User",
				"billing_type": "pro",
				"has_assigned_account": true,
				"created_at": "2024-01-01T00:00:00Z"
			}`)

		case path == "/api/auth/me":
			return mockHTTPResponse(200, "application/json", `{
				"account_uuid": "fake-uuid-0000-0000-000000000001",
				"email_address": "user@example.com"
			}`)

		case strings.HasPrefix(path, "/mcp-registry"):
			return mockHTTPResponse(200, "application/json", `{"servers":[]}`)

		case strings.HasPrefix(path, "/api/event_logging"):
			return mockHTTPResponse(200, "application/json", `{"ok":true}`)

		case strings.HasPrefix(path, "/api/claude_code/settings"):
			return mockHTTPResponse(200, "application/json", `{}`)

		case strings.HasPrefix(path, "/api/claude_code/policy_limits"):
			return mockHTTPResponse(200, "application/json", `{}`)

		case strings.HasPrefix(path, "/api/claude_cli/bootstrap"):
			return mockHTTPResponse(200, "application/json", `{"config":{}}`)

		case strings.HasPrefix(path, "/api/eval/"):
			return mockHTTPResponse(200, "application/json", `{"ok":true}`)

		case strings.HasPrefix(path, "/api/claude_code_penguin_mode"):
			return mockHTTPResponse(200, "application/json", `{"enabled":false}`)
		}
	}

	if host == "statsig.anthropic.com" {
		return mockHTTPResponse(200, "application/json", `{
			"feature_gates":{},
			"dynamic_configs":{},
			"layer_configs":{},
			"has_updates":false
		}`)
	}

	return mockHTTPResponse(200, "application/json", `{"ok":true}`)
}

func mockHTTPResponse(status int, contentType, body string) string {
	return fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), contentType, len(body), body)
}

// hostOnly strips the port from a host:port string.
func hostOnly(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return h
}

// bidiSplice copies data bidirectionally between two connections.
func bidiSplice(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
}

func closeWrite(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}

// stripHopByHop removes hop-by-hop headers from the response.
func stripHopByHop(h http.Header) {
	hopByHop := []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "TE", "Trailers",
		"Upgrade",
	}
	for _, k := range hopByHop {
		h.Del(k)
	}
}
