package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// Genius supplies the "About" text for a track, which agents use for their between-track patter.
// It's optional and best-effort: any failure just means no about line.

const maxAboutLen = 400

var geniusHTTP = &http.Client{
	Timeout:   8 * time.Second,
	Transport: &http.Transport{Proxy: proxyFunc()},
}

// proxyFunc honours HTTPS_PROXY, falling back to the macOS system proxy, since Genius is unreachable
// from some networks without one and CLI tools don't inherit the system setting.
func proxyFunc() func(*http.Request) (*url.URL, error) {
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" || runtime.GOOS != "darwin" {
		return http.ProxyFromEnvironment
	}
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return nil
	}
	field := func(k string) string {
		m := regexp.MustCompile(`(?m)^\s*` + k + ` : (\S+)`).FindSubmatch(out)
		if m == nil {
			return ""
		}
		return string(m[1])
	}
	if field("HTTPSEnable") != "1" || field("HTTPSProxy") == "" {
		return nil
	}
	u := &url.URL{Scheme: "http", Host: field("HTTPSProxy") + ":" + field("HTTPSPort")}
	return http.ProxyURL(u)
}

func geniusGet(token, path string, out any) error {
	req, _ := http.NewRequest("GET", "https://api.genius.com"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := geniusHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("genius %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// about returns the Genius description for a track, or "" if there isn't one.
func about(token string, t *Track) (string, error) {
	if len(t.Artists) == 0 {
		return "", nil
	}
	artist := t.Artists[0].Name
	// Spotify suffixes like " - 2010 Remaster" or " - Live" confuse Genius search
	title, _, _ := strings.Cut(t.Name, " - ")

	var search struct {
		Response struct {
			Hits []struct {
				Result struct {
					ID            int `json:"id"`
					PrimaryArtist struct {
						Name string `json:"name"`
					} `json:"primary_artist"`
				} `json:"result"`
			} `json:"hits"`
		} `json:"response"`
	}
	if err := geniusGet(token, "/search?per_page=5&q="+url.QueryEscape(artist+" "+title), &search); err != nil {
		return "", err
	}
	id := 0
	for _, h := range search.Response.Hits {
		if na, nh := norm(artist), norm(h.Result.PrimaryArtist.Name); na != "" && strings.Contains(nh, na) {
			id = h.Result.ID
			break
		}
	}
	if id == 0 {
		return "", nil
	}

	var song struct {
		Response struct {
			Song struct {
				Description struct {
					Plain string `json:"plain"`
				} `json:"description"`
			} `json:"song"`
		} `json:"response"`
	}
	if err := geniusGet(token, fmt.Sprintf("/songs/%d?text_format=plain", id), &song); err != nil {
		return "", err
	}
	d := strings.Join(strings.Fields(song.Response.Song.Description.Plain), " ")
	if d == "?" { // Genius's placeholder for "no description yet"
		return "", nil
	}
	if len(d) > maxAboutLen {
		d = d[:strings.LastIndex(d[:maxAboutLen], " ")] + "…"
	}
	return d, nil
}

// printAbout adds an about line for the track when Genius is configured and has something to say.
func printAbout(c *Client, t *Track) {
	if c.cfg.GeniusToken == "" || t == nil {
		return
	}
	if d, err := about(c.cfg.GeniusToken, t); err == nil && d != "" {
		fmt.Printf("about=%q\n", d)
	}
}

func cmdGeniusToken(args []string) error {
	var token string
	switch {
	case len(args) == 1:
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		token = string(b)
	case len(args) == 0 && term.IsTerminal(int(os.Stdin.Fd())):
		fmt.Print(`Genius gives spdj a short backstory for many tracks. To get a token:
  1. Sign in at https://genius.com/api-clients and click New API Client
     (any name; the redirect URI is unused, e.g. http://127.0.0.1:8888/callback)
  2. Click Generate Access Token and copy it (not the client ID or secret)

`)
		fmt.Print("Access token (input hidden): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		token = string(b)
	default:
		return fmt.Errorf("usage: spdj genius-token [FILE]  (prompts for the token when FILE is omitted)")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("no token entered")
	}
	var probe struct{}
	if err := geniusGet(token, "/search?per_page=1&q=test", &probe); err != nil {
		return fmt.Errorf("token check failed: %w", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		cfg = &Config{}
	}
	cfg.GeniusToken = token
	if err := cfg.save(); err != nil {
		return err
	}
	fmt.Println("genius token saved; wait/start/resume will now include about= lines")
	return nil
}
