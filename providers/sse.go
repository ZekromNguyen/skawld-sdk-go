package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

type sseResult struct {
	Event map[string]interface{}
	Err   error
}

func postSSE(ctx context.Context, client *http.Client, url string, headers map[string]string, payload interface{}) <-chan sseResult {
	out := make(chan sseResult)
	go func() {
		defer close(out)
		raw, err := json.Marshal(payload)
		if err != nil {
			sendSSEResult(ctx, out, sseResult{Err: err})
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			sendSSEResult(ctx, out, sseResult{Err: err})
			return
		}
		req.Header.Set("content-type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if client == nil {
			client = &http.Client{Timeout: 0}
		}
		resp, err := client.Do(req)
		if err != nil {
			sendSSEResult(ctx, out, sseResult{Err: err})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			sendSSEResult(ctx, out, sseResult{Err: providerHTTPError(resp.StatusCode, strings.TrimSpace(string(body)), resp.Header.Get("retry-after"))})
			return
		}
		sc := bufio.NewScanner(resp.Body)
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if !emitSSEData(ctx, data.String(), out) {
					return
				}
				data.Reset()
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if data.Len() > 0 {
			if !emitSSEData(ctx, data.String(), out) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			sendSSEResult(ctx, out, sseResult{Err: err})
		}
	}()
	return out
}

func providerHTTPError(status int, body, retryAfter string) *core.SkawldError {
	message, reason := providerErrorDetails(body)
	if message == "" {
		message = fmt.Sprintf("provider HTTP %d", status)
	}
	if body != "" && message == fmt.Sprintf("provider HTTP %d", status) {
		message += ": " + body
	}
	lower := strings.ToLower(message + " " + reason + " " + body)
	kind := core.ErrorProvider
	retryable := status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = core.ErrorAuth
		retryable = false
	case status == http.StatusTooManyRequests:
		kind = core.ErrorRateLimit
		retryable = true
	case strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "max context") ||
		strings.Contains(lower, "too many tokens"):
		kind = core.ErrorContextLength
		retryable = false
	}
	return &core.SkawldError{
		Kind:       kind,
		Message:    message,
		Retryable:  retryable,
		Status:     status,
		Reason:     reason,
		RetryAfter: parseRetryAfter(retryAfter),
	}
}

func providerErrorDetails(body string) (string, string) {
	var root map[string]interface{}
	if json.Unmarshal([]byte(body), &root) != nil {
		return "", ""
	}
	var message, reason string
	if errValue, ok := root["error"]; ok {
		switch errObj := errValue.(type) {
		case string:
			message = errObj
		case map[string]interface{}:
			message = stringValue(errObj["message"])
			reason = firstString(errObj["type"], errObj["code"], errObj["status"])
		}
	}
	if message == "" {
		message = firstString(root["message"], root["detail"])
	}
	if reason == "" {
		reason = firstString(root["type"], root["code"], root["status"])
	}
	return message, reason
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}
	t, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	d := time.Until(t)
	if d < 0 {
		return 0
	}
	return d
}

func firstString(values ...interface{}) string {
	for _, value := range values {
		if s := stringValue(value); s != "" {
			return s
		}
	}
	return ""
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	default:
		return ""
	}
}

func emitSSEData(ctx context.Context, data string, out chan<- sseResult) bool {
	if data == "" || data == "[DONE]" {
		return true
	}
	var obj map[string]interface{}
	if json.Unmarshal([]byte(data), &obj) != nil {
		return true
	}
	return sendSSEResult(ctx, out, sseResult{Event: obj})
}

func sendSSEResult(ctx context.Context, out chan<- sseResult, result sseResult) bool {
	select {
	case out <- result:
		return true
	default:
	}
	select {
	case out <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 0}
}

func sendProviderEvent(ctx context.Context, out chan<- core.ProviderStreamResult, ev core.ProviderStreamEvent) bool {
	return sendProviderResult(ctx, out, core.ProviderStreamResult{Event: ev})
}

func sendProviderError(ctx context.Context, out chan<- core.ProviderStreamResult, err error) bool {
	if err == nil {
		return true
	}
	return sendProviderResult(ctx, out, core.ProviderStreamResult{Err: err})
}

func sendProviderResult(ctx context.Context, out chan<- core.ProviderStreamResult, result core.ProviderStreamResult) bool {
	select {
	case out <- result:
		return true
	default:
	}
	select {
	case out <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(250*(1<<(attempt-1))) * time.Millisecond
	if d > 5*time.Second {
		return 5 * time.Second
	}
	return d
}
