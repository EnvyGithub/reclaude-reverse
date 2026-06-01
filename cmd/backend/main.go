// Backend server that receives forwarded Claude API requests.
//
// Modes:
//   - intercept (default): Log all requests, return mock responses
//   - forward: Inject API key and forward to real Anthropic API
//
// Usage:
//
//	backend.exe [-listen 127.0.0.1:8080] [-mode intercept] [-api-key sk-ant-...] [-anthropic-url https://api.anthropic.com]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"my-claude-proxy/internal/backend"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:8080", "Backend server listen address")
	mode := flag.String("mode", "intercept", "Operating mode: 'intercept' (log only) or 'forward' (proxy to Anthropic)")
	apiKey := flag.String("api-key", "", "Anthropic API key (方案A: x-api-key 模式)")
	oauthToken := flag.String("oauth-token", "", "Real OAuth token (方案B: Authorization Bearer 模式)")
	anthropicURL := flag.String("anthropic-url", "https://api.anthropic.com", "Anthropic API base URL")
	flag.Parse()

	// API key from flag or env
	key := *apiKey
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}

	// OAuth token from flag or env
	oauth := *oauthToken
	if oauth == "" {
		oauth = os.Getenv("REAL_OAUTH_TOKEN")
	}

	backendMode := backend.ModeIntercept
	if *mode == "forward" {
		backendMode = backend.ModeForward
	}

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║        My Claude Backend Server                      ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	if backendMode == backend.ModeIntercept {
		fmt.Println("  模式: 🔍 INTERCEPT (拦截 + 日志, 返回模拟响应)")
		fmt.Println("  所有请求都会被记录但不会转发到 Anthropic")
	} else {
		fmt.Println("  模式: 🔀 FORWARD (转发到 Anthropic API)")
		if oauth != "" {
			masked := oauth[:10] + "..." + oauth[len(oauth)-4:]
			fmt.Printf("  认证: 🔑 OAuth Bearer Token: %s\n", masked)
		} else if key != "" {
			maskedKey := key[:10] + "..." + key[len(key)-4:]
			fmt.Printf("  认证: 🔑 API Key: %s\n", maskedKey)
		} else {
			fmt.Println("  ⚠ 未设置认证 — 将透传原始 Authorization header")
		}
		fmt.Printf("  目标: %s\n", *anthropicURL)
	}
	fmt.Println()

	handler := backend.NewHandler(backendMode, key, oauth, *anthropicURL)

	log.Printf("[BACKEND] Starting on %s (mode: %s)", *listenAddr, *mode)
	fmt.Println()

	server := &http.Server{
		Addr:    *listenAddr,
		Handler: handler,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Backend server error: %v", err)
	}
}
