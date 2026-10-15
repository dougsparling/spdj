package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.spotify.com/v1"

type Client struct {
	cfg *Config
}

// call sends a Web API request. path is relative to /v1 or a full URL (for paging).
// A nil out discards the response body.
func (c *Client) call(method, path string, body, out any) error {
	if time.Now().After(c.cfg.Expiry) {
		if err := c.cfg.refresh(); err != nil {
			return err
		}
	}
	u := path
	if !strings.HasPrefix(u, "http") {
		u = apiBase + path
	}
	for attempt := 0; ; attempt++ {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, u, r)
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == 401 && attempt == 0:
			if err := c.cfg.refresh(); err != nil {
				return err
			}
			continue
		case resp.StatusCode == 429 && attempt < 3:
			wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			time.Sleep(time.Duration(max(wait, 1)) * time.Second)
			continue
		case resp.StatusCode >= 300:
			return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
		}
		// player endpoints sometimes answer 200 with a non-JSON body; ignore it
		if out != nil && json.Valid(data) {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("spotify %d: %s", e.Status, e.Body) }
