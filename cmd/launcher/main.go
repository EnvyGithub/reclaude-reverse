// Launcher tool: kills existing Claude Desktop, then restarts it
// with proxy environment variables injected — mimicking ReClaude's
// spawn.ClaudeApp behavior.
//
// Usage:
//
//	launcher.exe [-proxy 127.0.0.1:9090] [-ca-cert path/to/ca.crt]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	proxyAddr := flag.String("proxy", "127.0.0.1:9090", "Local MITM proxy address")
	caCertPath := flag.String("ca-cert", "", "Path to CA certificate (auto-detected if empty)")
	flag.Parse()

	// Auto-detect CA cert path
	caPath := *caCertPath
	if caPath == "" {
		home, _ := os.UserHomeDir()
		caPath = filepath.Join(home, ".my-claude-proxy", "ca.crt")
	}

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  Claude Desktop Launcher (带代理注入)                ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// Step 1: Find Claude Desktop App executable
	claudePath := findClaudeDesktop()
	if claudePath == "" {
		log.Fatal("[ERROR] 未找到 Claude Desktop App！请先安装。")
	}
	fmt.Printf("  Claude 路径: %s\n", claudePath)

	// Step 2: Kill existing Claude processes
	fmt.Println()
	fmt.Println("[1/3] 关闭已运行的 Claude Desktop...")
	killExisting()
	time.Sleep(2 * time.Second)
	fmt.Println("  ✓ Claude Desktop 已关闭")

	// Step 3: Build environment with proxy settings
	// This mimics ReClaude's spawn.claudeProxyEnv()
	fmt.Println()
	fmt.Println("[2/3] 准备代理环境变量...")
	env := buildProxyEnv(*proxyAddr, caPath)
	for _, e := range env {
		if strings.HasPrefix(e, "HTTPS_PROXY=") ||
			strings.HasPrefix(e, "HTTP_PROXY=") ||
			strings.HasPrefix(e, "NODE_TLS_REJECT_UNAUTHORIZED=") ||
			strings.HasPrefix(e, "SSL_CERT_FILE=") ||
			strings.HasPrefix(e, "NO_PROXY=") {
			fmt.Printf("  %s\n", e)
		}
	}

	// Step 4: Launch Claude Desktop with proxy env
	fmt.Println()
	fmt.Println("[3/3] 启动 Claude Desktop (带代理)...")
	cmd := exec.Command(claudePath)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("[ERROR] 启动 Claude Desktop 失败: %v", err)
	}

	fmt.Println()
	fmt.Println("  ✓ Claude Desktop 已启动 (PID:", cmd.Process.Pid, ")")
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  ✓ Claude Desktop 正在通过代理运行                    ║")
	fmt.Printf("║  代理地址: http://%s                    ║\n", *proxyAddr)
	fmt.Println("║  所有 api.anthropic.com 流量将被拦截               ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("提示: 查看 backend.exe 窗口观察拦截到的请求")
}

// buildProxyEnv builds environment variables mimicking ReClaude's spawn.claudeProxyEnv()
func buildProxyEnv(proxyAddr, caCertPath string) []string {
	env := os.Environ()

	proxyURL := "http://" + proxyAddr

	// Core proxy settings — same as ReClaude
	env = upsertEnv(env, "HTTPS_PROXY", proxyURL)
	env = upsertEnv(env, "HTTP_PROXY", proxyURL)
	env = upsertEnv(env, "https_proxy", proxyURL) // lowercase variant
	env = upsertEnv(env, "http_proxy", proxyURL)  // lowercase variant

	// Tell Node.js to accept self-signed CA certificate
	env = upsertEnv(env, "NODE_TLS_REJECT_UNAUTHORIZED", "0")

	// Set SSL certificate paths — same as ReClaude
	if caCertPath != "" {
		if _, err := os.Stat(caCertPath); err == nil {
			env = upsertEnv(env, "SSL_CERT_FILE", caCertPath)
			env = upsertEnv(env, "REQUESTS_CA_BUNDLE", caCertPath)
			env = upsertEnv(env, "NODE_EXTRA_CA_CERTS", caCertPath)
		}
	}

	// Prevent proxy loops — same as ReClaude
	env = upsertEnv(env, "NO_PROXY", "127.0.0.1,localhost")
	env = upsertEnv(env, "no_proxy", "127.0.0.1,localhost")

	return env
}

// findClaudeDesktop locates the Claude Desktop App executable on Windows
func findClaudeDesktop() string {
	// Check WindowsApps (Store apps)
	windowsApps := filepath.Join(os.Getenv("ProgramFiles"), "WindowsApps")
	matches, _ := filepath.Glob(filepath.Join(windowsApps, "Claude_*", "app", "Claude.exe"))
	if len(matches) > 0 {
		return matches[len(matches)-1] // Use the latest version
	}

	// Check common installation paths
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Claude", "Claude.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "claude-desktop", "Claude.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Claude", "Claude.exe"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}

// killExisting kills all running Claude Desktop processes
func killExisting() {
	exec.Command("taskkill", "/IM", "Claude.exe", "/F").Run()
	// Also try lowercase
	exec.Command("taskkill", "/IM", "claude.exe", "/F").Run()
}

// upsertEnv sets or updates an environment variable
func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
