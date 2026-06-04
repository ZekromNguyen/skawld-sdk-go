package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func postSSE(ctx context.Context, client *http.Client, url string, headers map[string]string, payload interface{}) (<-chan map[string]interface{}, <-chan error) {
	out := make(chan map[string]interface{})
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		raw, err := json.Marshal(payload)
		if err != nil {
			errs <- err
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			errs <- err
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
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			errs <- fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return
		}
		sc := bufio.NewScanner(resp.Body)
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				emitSSEData(ctx, data.String(), out)
				data.Reset()
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if data.Len() > 0 {
			emitSSEData(ctx, data.String(), out)
		}
		if err := sc.Err(); err != nil {
			errs <- err
		}
	}()
	return out, errs
}

func emitSSEData(ctx context.Context, data string, out chan<- map[string]interface{}) {
	if data == "" || data == "[DONE]" {
		return
	}
	var obj map[string]interface{}
	if json.Unmarshal([]byte(data), &obj) != nil {
		return
	}
	select {
	case out <- obj:
	case <-ctx.Done():
	}
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 0}
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
