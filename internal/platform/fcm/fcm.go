package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const scope = "https://www.googleapis.com/auth/firebase.messaging"

type Client struct {
	enabled   bool
	projectID string
	http      *http.Client
	log       *slog.Logger
}

func New(ctx context.Context, credentialsFile, projectID string, log *slog.Logger) (*Client, error) {
	if strings.TrimSpace(credentialsFile) == "" {
		return &Client{enabled: false, log: log}, nil
	}

	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("read fcm credentials: %w", err)
	}

	creds, err := google.CredentialsFromJSON(ctx, data, scope)
	if err != nil {
		return nil, fmt.Errorf("parse fcm credentials: %w", err)
	}
	if projectID == "" {
		projectID = creds.ProjectID
	}
	if projectID == "" {
		return nil, fmt.Errorf("fcm project id is required")
	}

	hc := oauth2.NewClient(ctx, creds.TokenSource)
	hc.Timeout = 15 * time.Second
	return &Client{enabled: true, projectID: projectID, http: hc, log: log}, nil
}

func (c *Client) Enabled() bool { return c != nil && c.enabled }

type sendResponse struct {
	Error *struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

func (c *Client) Send(ctx context.Context, tokens []string, title, body string, data map[string]string) ([]string, int, error) {
	if !c.Enabled() || len(tokens) == 0 {
		return nil, 0, nil
	}

	var invalid []string
	var sent int
	var firstErr error

	for _, token := range tokens {
		ok, invalidToken, err := c.sendOne(ctx, token, title, body, data)

		if invalidToken {
			invalid = append(invalid, token)
		}

		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.log.Warn("fcm send failed", "error", err)
			continue
		}

		if !ok {
			continue
		}
		sent++
	}

	return invalid, sent, firstErr
}

func (c *Client) sendOne(ctx context.Context, token, title, body string, data map[string]string) (bool, bool, error) {
	payload := map[string]any{
		"message": map[string]any{
			"token": token,
			"notification": map[string]string{
				"title": title,
				"body":  body,
			},
			"data": data,
			"android": map[string]any{
				"priority": "high",
				"notification": map[string]any{
					"channel_id": "fire_alerts",
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, false, err
	}

	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", c.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return false, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		io.Copy(io.Discard, resp.Body)
		return true, false, nil
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var sr sendResponse
	_ = json.Unmarshal(bodyBytes, &sr)

	invalid := resp.StatusCode == http.StatusNotFound
	if sr.Error != nil {
		for _, d := range sr.Error.Details {
			if d.ErrorCode == "UNREGISTERED" || d.ErrorCode == "INVALID_ARGUMENT" {
				invalid = true
			}
		}
	}
	msg := string(bodyBytes)
	if sr.Error != nil && sr.Error.Message != "" {
		msg = sr.Error.Message
	}
	return false, invalid, fmt.Errorf("fcm status %d: %s", resp.StatusCode, msg)
}
