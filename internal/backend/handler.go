// Package backend implements the local backend server that receives
// forwarded requests from the MITM proxy.
//
// In "intercept" mode: logs all request details without forwarding.
// In "forward" mode: injects API key and forwards to Anthropic.
package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Mode defines the backend operating mode.
type Mode string

const (
	ModeIntercept Mode = "intercept" // Log only, return mock response
	ModeForward   Mode = "forward"   // Forward to real Anthropic API
)

// Handler is the backend HTTP handler.
type Handler struct {
	mode           Mode
	apiKey         string
	oauthToken     string
	anthropicURL   string
	requestCount   int
}

// NewHandler creates a new backend handler.
func NewHandler(mode Mode, apiKey, oauthToken, anthropicURL string) *Handler {
	if anthropicURL == "" {
		anthropicURL = "https://api.anthropic.com"
	}
	return &Handler{
		mode:         mode,
		apiKey:       apiKey,
		oauthToken:   oauthToken,
		anthropicURL: anthropicURL,
	}
}

// ServeHTTP handles incoming requests from the MITM proxy.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requestCount++
	startTime := time.Now()
	reqNum := h.requestCount

	originalHost := r.Header.Get("X-Original-Host")
	if originalHost == "" {
		originalHost = "api.anthropic.com"
	}

	// ═══════════════════════════════════════════════════════
	// Log request details
	// ═══════════════════════════════════════════════════════
	log.Printf("╔══════════════════════════════════════════════════════╗")
	log.Printf("║ REQUEST #%d                                          ", reqNum)
	log.Printf("╚══════════════════════════════════════════════════════╝")
	log.Printf("[REQ #%d] %s %s://%s%s", reqNum, r.Method, "https", originalHost, r.URL.Path)
	log.Printf("[REQ #%d] Time: %s", reqNum, startTime.Format("2006-01-02 15:04:05.000"))

	// Log all headers
	log.Printf("[REQ #%d] ─── Headers ───", reqNum)
	for k, vv := range r.Header {
		for _, v := range vv {
			// Mask sensitive values
			displayVal := v
			lk := strings.ToLower(k)
			if lk == "x-api-key" || lk == "authorization" || lk == "cookie" {
				if len(v) > 20 {
					displayVal = v[:12] + "..." + v[len(v)-4:]
				}
			}
			log.Printf("[REQ #%d]   %s: %s", reqNum, k, displayVal)
		}
	}

	// Read and log request body
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}

	if len(bodyBytes) > 0 {
		log.Printf("[REQ #%d] ─── Body (%d bytes) ───", reqNum, len(bodyBytes))

		// Try to pretty-print JSON
		var bodyJSON interface{}
		if json.Unmarshal(bodyBytes, &bodyJSON) == nil {
			pretty, _ := json.MarshalIndent(bodyJSON, "  ", "  ")
			// Truncate if too long
			bodyStr := string(pretty)
			if len(bodyStr) > 2000 {
				bodyStr = bodyStr[:2000] + "\n  ... (truncated)"
			}
			log.Printf("[REQ #%d]   %s", reqNum, bodyStr)
		} else {
			bodyStr := string(bodyBytes)
			if len(bodyStr) > 500 {
				bodyStr = bodyStr[:500] + "... (truncated)"
			}
			log.Printf("[REQ #%d]   %s", reqNum, bodyStr)
		}
	}

	// ═══════════════════════════════════════════════════════
	// Handle based on mode
	// ═══════════════════════════════════════════════════════
	switch h.mode {
	case ModeIntercept:
		h.handleIntercept(w, r, reqNum, bodyBytes)
	case ModeForward:
		h.handleForward(w, r, reqNum, bodyBytes)
	}

	elapsed := time.Since(startTime)
	log.Printf("[REQ #%d] ─── Done (%.1fms) ───", reqNum, float64(elapsed.Microseconds())/1000.0)
	log.Printf("")
}

// handleIntercept logs the request and returns a mock response.
func (h *Handler) handleIntercept(w http.ResponseWriter, r *http.Request, reqNum int, body []byte) {
	log.Printf("[REQ #%d] [MODE: INTERCEPT] Returning mock response", reqNum)

	// Check what kind of request this is
	path := r.URL.Path

	switch {
	case strings.HasSuffix(path, "/v1/messages"):
		// Claude API messages endpoint — return a mock streaming response
		h.mockMessagesResponse(w, reqNum, body)

	case strings.Contains(path, "/health"):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))

	default:
		// Generic mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		resp := map[string]interface{}{
			"intercepted": true,
			"request_num": reqNum,
			"path":        path,
			"method":      r.Method,
			"message":     "Request intercepted by my-claude-proxy backend",
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		w.Write(data)
	}
}

// mockMessagesResponse returns a mock Claude API streaming response (SSE format).
func (h *Handler) mockMessagesResponse(w http.ResponseWriter, reqNum int, body []byte) {
	log.Printf("[REQ #%d] 🎯 Intercepted /v1/messages — returning mock streaming response", reqNum)

	// Check if streaming is requested
	var reqBody map[string]interface{}
	isStreaming := true
	model := "claude-sonnet-4-20250514"
	if json.Unmarshal(body, &reqBody) == nil {
		if s, ok := reqBody["stream"].(bool); ok {
			isStreaming = s
		}
		if m, ok := reqBody["model"].(string); ok {
			model = m
		}
	}

	mockText := fmt.Sprintf("[INTERCEPTED by my-claude-proxy] 模拟响应 #%d — 流量已被成功劫持！", reqNum)
	msgID := fmt.Sprintf("msg_mock_%d_%d", reqNum, time.Now().Unix())

	if !isStreaming {
		// Non-streaming response
		resp := map[string]interface{}{
			"id":    msgID,
			"type":  "message",
			"role":  "assistant",
			"model": model,
			"content": []map[string]interface{}{
				{"type": "text", "text": mockText},
			},
			"stop_reason": "end_turn",
			"usage":       map[string]interface{}{"input_tokens": 10, "output_tokens": 30},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		data, _ := json.Marshal(resp)
		w.Write(data)
		return
	}

	// SSE streaming response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	flusher, canFlush := w.(http.Flusher)
	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if canFlush {
			flusher.Flush()
		}
	}

	// 1. message_start
	send("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"%s","type":"message","role":"assistant","content":[],"model":"%s","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`, msgID, model))

	// 2. content_block_start
	send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)

	// 3. content_block_delta — send text in chunks for realism
	for i, ch := range mockText {
		_ = i
		send("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%s"}}`, string(ch)))
	}

	// 4. content_block_stop
	send("content_block_stop", `{"type":"content_block_stop","index":0}`)

	// 5. message_delta
	send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":30}}`)

	// 6. message_stop
	send("message_stop", `{"type":"message_stop"}`)
}

// handleForward forwards the request to the real Anthropic API.
func (h *Handler) handleForward(w http.ResponseWriter, r *http.Request, reqNum int, body []byte) {
	log.Printf("[REQ #%d] [MODE: FORWARD] Forwarding to %s", reqNum, h.anthropicURL)

	// Build target URL
	targetURL := h.anthropicURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Create forward request
	fwdReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, strings.NewReader(string(body)))
	if err != nil {
		log.Printf("[REQ #%d] ✗ Create request error: %v", reqNum, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy headers
	for k, vv := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-original-") ||
			strings.HasPrefix(strings.ToLower(k), "x-forwarded-") {
			continue
		}
		for _, v := range vv {
			fwdReq.Header.Add(k, v)
		}
	}

	// Inject credentials
	if h.oauthToken != "" {
		// 方案 B: 用真实 OAuth Token 替换 Authorization Bearer
		fwdReq.Header.Set("Authorization", "Bearer "+h.oauthToken)
		// 保留 Anthropic-Dangerous-Direct-Browser-Access: true
		log.Printf("[REQ #%d] 🔑 Replaced Authorization with real OAuth token", reqNum)
	} else if h.apiKey != "" {
		// 方案 A: 用 API Key
		fwdReq.Header.Set("x-api-key", h.apiKey)
		fwdReq.Header.Del("Authorization")
		fwdReq.Header.Del("Anthropic-Dangerous-Direct-Browser-Access")
	}

	if fwdReq.Header.Get("anthropic-version") == "" {
		fwdReq.Header.Set("anthropic-version", "2023-06-01")
	}
	fwdReq.Host = "api.anthropic.com"

	// Send request
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(fwdReq)
	if err != nil {
		log.Printf("[REQ #%d] ✗ Forward error: %v", reqNum, err)
		http.Error(w, fmt.Sprintf("Forward error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("[REQ #%d] → Response: %d %s", reqNum, resp.StatusCode, http.StatusText(resp.StatusCode))

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream response body
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		flusher, ok := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if ok {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
}
