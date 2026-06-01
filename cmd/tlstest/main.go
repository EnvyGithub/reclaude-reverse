package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
)

func main() {
	url := "https://claude.ai/healthcheck"

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║   TLS 指纹对比测试: Go 原生 vs utls Chrome 模拟     ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// ── Test 1: Go default TLS ──
	fmt.Println("━━━ Test 1: Go 原生 HTTP client ━━━")
	status1, body1 := testDefault(url)
	fmt.Printf("  Status: %d\n", status1)
	fmt.Printf("  Body:   %s\n\n", trunc(body1, 150))

	// ── Test 2: utls Chrome fingerprint, HTTP/1.1 only ──
	fmt.Println("━━━ Test 2: utls Chrome 指纹 (HTTP/1.1) ━━━")
	status2, body2 := testChrome(url)
	fmt.Printf("  Status: %d\n", status2)
	fmt.Printf("  Body:   %s\n\n", trunc(body2, 150))

	// ── Summary ──
	fmt.Println("━━━ 结论 ━━━")
	if status1 == 403 && (status2 == 200 || status2 == 301 || status2 == 302) {
		fmt.Println("  ✅ Go 原生=403(被拦截), utls Chrome 通过! → TLS 指纹伪装有效!")
	} else if status1 == 403 && status2 == 403 {
		fmt.Println("  ❌ 两个都被拦截 — Cloudflare 可能还检测了其他因素")
	} else {
		fmt.Printf("  Go=%d, utls=%d\n", status1, status2)
	}
}

func testDefault(url string) (int, string) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{},
		// Force HTTP/1.1 for fair comparison
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func testChrome(url string) (int, string) {
	tr := &http.Transport{
		DialTLS: func(network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			if err != nil {
				return nil, err
			}

			host, _, _ := net.SplitHostPort(addr)

			// Build Chrome spec but force HTTP/1.1 ALPN
			spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("spec error: %v", err)
			}

			// Modify ALPN to only include http/1.1
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
				return nil, fmt.Errorf("apply preset: %v", err)
			}

			if err := uconn.Handshake(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("handshake: %v", err)
			}

			fmt.Printf("  ALPN negotiated: %q\n", uconn.ConnectionState().NegotiatedProtocol)
			return uconn, nil
		},
		// Disable Go's built-in HTTP/2
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}

	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
