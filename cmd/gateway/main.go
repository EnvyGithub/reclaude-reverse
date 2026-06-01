package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"my-claude-proxy/internal/gateway"
)

func main() {
	configPath := flag.String("config", "config/gateway.json", "Path to gateway config file")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║        Claude Sharing Gateway                       ║")
	fmt.Println("║        Multi-user token pool + SK auth              ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// Load config
	cfg, err := gateway.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Resolve SK file path relative to config dir
	skPath := cfg.SKFile
	if !filepath.IsAbs(skPath) {
		skPath = filepath.Join(filepath.Dir(*configPath), skPath)
	}

	// Initialize token pool
	pool := gateway.NewTokenPool(cfg.Tokens)

	// Initialize SK store
	skStore, err := gateway.NewSKStore(skPath)
	if err != nil {
		log.Fatalf("Failed to load SK store: %v", err)
	}

	// Create server
	srv := gateway.NewServer(pool, skStore)

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", srv.HandleStats)  // GET /stats — usage dashboard
	mux.Handle("/", srv)                       // Everything else — proxy

	listen := cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:8443"
	}

	log.Printf("[GATEWAY] Listening on %s", listen)
	log.Printf("[GATEWAY] Token pool: %d accounts", len(cfg.Tokens))

	if err := http.ListenAndServe(listen, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func init() {
	// Ensure config and data dirs exist
	os.MkdirAll("config", 0755)
	os.MkdirAll("data", 0755)
}
