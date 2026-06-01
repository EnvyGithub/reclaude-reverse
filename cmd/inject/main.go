// Tool to inject fake credentials into Claude Code's credential file,
// making Claude Code believe it's already logged in.
// This mimics ReClaude's WriteClaudeCredentials behavior.
//
// Usage:
//
//	inject-credentials.exe [-sk <session_key>]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

func main() {
	sk := flag.String("sk", "", "Session key to inject (if empty, a random one is generated)")
	flag.Parse()

	// Generate a fake SK if not provided (43 chars base64url, same format as ReClaude)
	sessionKey := *sk
	if sessionKey == "" {
		sessionKey = generateFakeSK()
	}

	home, _ := os.UserHomeDir()
	claudeDir := filepath.Join(home, ".claude")
	credPath := filepath.Join(claudeDir, "credentials.json")

	// Ensure ~/.claude/ directory exists
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		fmt.Printf("[ERROR] Failed to create %s: %v\n", claudeDir, err)
		os.Exit(1)
	}

	// Backup existing credentials if present
	if _, err := os.Stat(credPath); err == nil {
		backupPath := credPath + ".backup." + time.Now().Format("20060102_150405")
		data, _ := os.ReadFile(credPath)
		os.WriteFile(backupPath, data, 0600)
		fmt.Printf("[BACKUP] Existing credentials backed up to: %s\n", backupPath)
	}

	// Build the credential structure that Claude Code expects
	// This mimics what ReClaude's WriteClaudeCredentials does
	creds := map[string]interface{}{
		"claudeAiOauth": map[string]interface{}{
			"token": sessionKey,
		},
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		fmt.Printf("[ERROR] Failed to marshal credentials: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(credPath, data, 0600); err != nil {
		fmt.Printf("[ERROR] Failed to write credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║        伪凭证注入成功                                ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  凭证文件: %s\n", credPath)
	fmt.Printf("  Session Key: %s...%s\n", sessionKey[:8], sessionKey[len(sessionKey)-4:])
	fmt.Println()
	fmt.Println("  Claude Code 现在会认为自己已经登录。")
	fmt.Println("  启动代理后，通过 HTTPS_PROXY 环境变量劫持流量即可。")
	fmt.Println()

	// Also write a minimal settings.json if not present
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		settings := map[string]interface{}{
			"permissions": map[string]interface{}{},
			"env":         map[string]interface{}{},
		}
		settingsData, _ := json.MarshalIndent(settings, "", "  ")
		os.WriteFile(settingsPath, settingsData, 0600)
		fmt.Printf("  [附加] 已创建 settings.json: %s\n", settingsPath)
	}
}

// generateFakeSK generates a random 43-character base64url session key
// matching ReClaude's SK format: ^[A-Za-z0-9_-]{43}$
func generateFakeSK() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	b := make([]byte, 43)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}
	return string(b)
}
