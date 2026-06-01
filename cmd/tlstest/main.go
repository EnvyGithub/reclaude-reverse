package main

import (
    "fmt"
    "io"
    "net"
    "net/http"
    "crypto/tls"
    "time"
    utls "github.com/refraction-networking/utls"
)

func main() {
    urls := []string{
        "https://claude.ai/healthcheck",
        "https://claude.ai/",
    }
    for _, url := range urls {
        fmt.Printf("=== %s ===\n", url)
        status, size := testChrome(url)
        fmt.Printf("  Status: %d, Size: %d bytes\n\n", status, size)
    }
}

func testChrome(url string) (int, int) {
    tr := &http.Transport{
        DialTLS: func(network, addr string) (net.Conn, error) {
            conn, _ := net.DialTimeout("tcp", addr, 10*time.Second)
            host, _, _ := net.SplitHostPort(addr)
            spec, _ := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
            for i, ext := range spec.Extensions {
                if alpn, ok := ext.(*utls.ALPNExtension); ok {
                    alpn.AlpnProtocols = []string{"http/1.1"}
                    spec.Extensions[i] = alpn
                }
            }
            uconn := utls.UClient(conn, &utls.Config{ServerName: host}, utls.HelloCustom)
            uconn.ApplyPreset(&spec)
            uconn.Handshake()
            return uconn, nil
        },
        TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
        DisableCompression: true,
    }
    c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
    resp, err := c.Do(req)
    if err != nil { fmt.Println("  Error:", err); return 0, 0 }
    defer resp.Body.Close()
    b, _ := io.ReadAll(resp.Body)
    return resp.StatusCode, len(b)
}
