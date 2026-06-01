// MITM Proxy for Claude Code / Claude Desktop App.
// Intercepts HTTPS traffic to api.anthropic.com and forwards to a local backend.
//
// Usage:
//
//	proxy.exe [-listen 127.0.0.1:9090] [-backend 127.0.0.1:8080]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"my-claude-proxy/internal/ca"
	"my-claude-proxy/internal/proxy"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:9090", "Proxy listen address")
	backendAddr := flag.String("backend", "127.0.0.1:8080", "Backend server address to forward intercepted requests")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║        My Claude MITM Proxy                         ║")
	fmt.Println("║        Intercepts api.anthropic.com traffic          ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// Step 1: Ensure CA certificate exists
	log.Println("[MAIN] Initializing CA certificate...")
	caRoot, err := ca.EnsureRoot()
	if err != nil {
		log.Fatalf("Failed to initialize CA: %v", err)
	}
	log.Printf("[MAIN] CA certificate ready: %s", ca.CAPath())
	log.Printf("[MAIN] ⚠  Please install the CA certificate to your system trust store!")
	log.Printf("[MAIN]    Run: powershell -File install-ca.ps1")
	fmt.Println()

	// Step 2: Create and start the proxy server
	server := proxy.NewServer(caRoot, *backendAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("[MAIN] Received signal %v, shutting down...", sig)
		cancel()
	}()

	log.Printf("[MAIN] Starting MITM proxy on %s", *listenAddr)
	log.Printf("[MAIN] Forwarding to backend: %s", *backendAddr)
	fmt.Println()
	fmt.Println("To use with Claude Code, run in another terminal:")
	fmt.Printf("  $env:HTTPS_PROXY = \"http://%s\"\n", *listenAddr)
	fmt.Println("  $env:NODE_TLS_REJECT_UNAUTHORIZED = \"0\"")
	fmt.Println("  claude")
	fmt.Println()

	if err := server.ListenAndServe(ctx, *listenAddr); err != nil && err != context.Canceled {
		log.Fatalf("Proxy server error: %v", err)
	}
}
