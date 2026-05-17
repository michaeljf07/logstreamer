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
	"strings"
	"time"
)

const label = "supabase"

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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	s.PrintSystemMessage(label, "Started polling logs (Management API)…")

	dedup := newDeduper(2000)
	client := &http.Client{Timeout: 30 * time.Second}
	apiURL := fmt.Sprintf("https://api.supabase.com/v1/projects/%s/analytics/endpoints/logs.all", projectREF)

	pollOnce(ctx, client, apiURL, accessToken, s, dedup)

	for {
		select {
		case <-ctx.Done():
			s.PrintSystemMessage(label, "Stopped polling logs.")
			return
		case <-ticker.C:
			pollOnce(ctx, client, apiURL, accessToken, s, dedup)
		}
	}
}

func pollOnce(ctx context.Context, client *http.Client, apiURL string, accessToken string, s *streamer.Streamer, dedup *deduper) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Failed to create request: %v", err))
		return
	}
	req.Header.Set("Authorization", "Bearer " + accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Network error: %v", err))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Read body: %v", err))
		return
	}

	if resp.StatusCode != http.StatusOK {
		s.PrintSystemMessage(label, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
		return
	}

	var env LogResponse
	if err := json.Unmarshal(body, &env); err != nil {
		s.PrintSystemMessage(label, fmt.Sprintf("Failed to parse JSON: %v", err))
		return
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
