package supabase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"logstreamer/streamer"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const label = "supabase"

const defaultPollInterval = 8 * time.Second

// Minimum and maximum backoff times for 429 responses from the Management API (to avoid rate limits)
const min429Backoff = 15 * time.Second
const max429Backoff = 3 * time.Minute

type pollResult int

const (
	pollOK pollResult = iota
	pollRateLimited
	pollFailed
)

type LogResponse struct {
	Result []json.RawMessage `json:"result"`
	Error  *string           `json:"error"`
}

type logRow struct {
	Timestamp    string `json:"timestamp"`
	EventMessage string `json:"event_message"`
	Message      string `json:"message"`
}

func ParseProjectRef(urlOrRef string) string {
	s := strings.TrimSpace(urlOrRef)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err == nil && u.Host != "" {
		host := u.Hostname()
		if cut := strings.Index(host, ".supabase.co"); cut > 0 {
			return host[:cut]
		}
	}
	return s
}

func AddSupabaseCommand(ctx context.Context, s *streamer.Streamer, urlOrRef string, accessToken string) error {
	ref := ParseProjectRef(urlOrRef)
	if ref == "" || accessToken == "" {
		return errors.New("project URL/ref and access token are required")
	}

	go PollLogs(ctx, ref, strings.TrimSpace(accessToken), s)
	return nil
}

// PollLogs requests logs periodically until ctx is cancelled, deduplicating lines across polls.
func PollLogs(ctx context.Context, projectREF string, accessToken string, s *streamer.Streamer) {
	s.PrintSystemMessage(label, "Started polling logs (Management API)…")

	dedup := newDeduper(2000)
	client := &http.Client{Timeout: 30 * time.Second}
	apiURL := fmt.Sprintf("https://api.supabase.com/v1/projects/%s/analytics/endpoints/logs.all", projectREF)

	wait := time.Duration(0)
	backoff := min429Backoff

	for {
		if !sleepOrDone(ctx, wait) {
			s.PrintSystemMessage(label, "Stopped polling logs.")
			return
		}

		retryAfter, result, body := pollOnce(ctx, client, apiURL, accessToken, s, dedup)
		switch result {
		case pollRateLimited:
			wait = maxDuration(retryAfter, backoff)
			backoff = minDuration(backoff*2, max429Backoff)
			s.PrintSystemMessage(label, fmt.Sprintf("Rate limited (429): %s — next poll in %v.", truncate(body, 400), wait))
		default:
			wait = defaultPollInterval
			backoff = min429Backoff
		}
	}
}

func sleepOrDone(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// pollOnce returns suggested wait before retry (from Retry-After when 429), the poll result, and the 429 body for logging.
func pollOnce(
	ctx context.Context, client *http.Client, apiURL string, accessToken string, s *streamer.Streamer, dedup *deduper,
) (
	next time.Duration,
	result pollResult,
	bodyText string,
) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Failed to create request: %v", err))
		return 0, pollFailed, ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Network error: %v", err))
		return 0, pollFailed, ""
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Read body: %v", err))
		return 0, pollFailed, ""
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return retryAfterFromHeader(resp.Header), pollRateLimited, string(rawBody)
	}

	if resp.StatusCode != http.StatusOK {
		s.PrintSystemMessage(label, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(rawBody)))
		return 0, pollFailed, ""
	}

	var env LogResponse
	if err := json.Unmarshal(rawBody, &env); err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Failed to parse JSON: %v", err))
		return 0, pollFailed, ""
	}
	if env.Error != nil && *env.Error != "" {
		s.PrintSystemMessage(label, fmt.Sprintf("API error field: %s", *env.Error))
	}

	for _, raw := range env.Result {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		line := formatLogRow(raw, dedup)
		if line == "" {
			continue
		}
		s.PrintIntegrationLog(label, "stdout", line)
	}
	return 0, pollOK, ""
}

// retryAfterFromHeader parses Retry-After (delta-seconds or HTTP-date). Returns 0 if unset/invalid.
func retryAfterFromHeader(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return clamp429Backoff(time.Duration(secs) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return clamp429Backoff(time.Until(t))
	}
	return 0
}

func clamp429Backoff(d time.Duration) time.Duration {
	if d < min429Backoff {
		return min429Backoff
	}
	if d > max429Backoff {
		return max429Backoff
	}
	return d
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func formatLogRow(raw []byte, dedup *deduper) string {
	var row logRow
	if err := json.Unmarshal(raw, &row); err != nil {
		fp := string(raw)
		if !dedup.mark(fp) {
			return ""
		}
		return fp
	}

	msg := row.EventMessage
	if msg == "" {
		msg = row.Message
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	ts := strings.TrimSpace(row.Timestamp)
	fp := ts + "\x00" + msg
	if !dedup.mark(fp) {
		return ""
	}
	if ts == "" {
		return msg
	}
	return ts + " " + msg
}

type deduper struct {
	queue []string
	set   map[string]struct{}
	limit int
}

func newDeduper(limit int) *deduper {
	return &deduper{
		set:   make(map[string]struct{}),
		limit: limit,
	}
}

// mark reports whether fp is new; if so, it records fp for future duplicate suppression.
func (d *deduper) mark(fp string) bool {
	if fp == "" {
		return false
	}
	if _, ok := d.set[fp]; ok {
		return false
	}
	d.set[fp] = struct{}{}
	d.queue = append(d.queue, fp)
	for len(d.queue) > d.limit {
		old := d.queue[0]
		d.queue = d.queue[1:]
		delete(d.set, old)
	}
	return true
}
