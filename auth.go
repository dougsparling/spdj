package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const scopes = "user-read-private user-read-playback-state user-modify-playback-state user-read-currently-playing " +
	"playlist-read-private playlist-modify-private playlist-modify-public"

// authorize runs the OAuth authorization-code flow with PKCE, so no client secret is needed.
func authorize(c *Config) error {
	ru, err := url.Parse(c.RedirectURI)
	if err != nil {
		return err
	}
	verifier := randString(64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randString(16)

	q := url.Values{
		"client_id":             {c.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {c.RedirectURI},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
	}
	authURL := "https://accounts.spotify.com/authorize?" + q.Encode()

	host := ru.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, ru.Port()))
	if err != nil {
		return fmt.Errorf("listen for callback: %w", err)
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(ru.Path, func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		switch {
		case v.Get("error") != "":
			errCh <- fmt.Errorf("spotify: %s", v.Get("error"))
		case v.Get("state") != state:
			errCh <- fmt.Errorf("state mismatch")
		default:
			codeCh <- v.Get("code")
		}
		fmt.Fprintln(w, "spdj: done, you can close this tab.")
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Println("Opening browser for Spotify login. If it doesn't open, visit:\n" + authURL)
	openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timed out waiting for login")
	}
	return c.tokenRequest(url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURI},
		"client_id":     {c.ClientID},
		"code_verifier": {verifier},
	})
}

func (c *Config) refresh() error {
	return c.tokenRequest(url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {c.ClientID},
	})
}

func (c *Config) tokenRequest(form url.Values) error {
	resp, err := http.PostForm("https://accounts.spotify.com/api/token", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	if t.Error != "" {
		return fmt.Errorf("token: %s: %s", t.Error, t.ErrorDesc)
	}
	c.AccessToken = t.AccessToken
	if t.RefreshToken != "" { // refreshes may or may not rotate it
		c.RefreshToken = t.RefreshToken
	}
	c.Expiry = time.Now().Add(time.Duration(t.ExpiresIn-60) * time.Second)
	return c.save()
}

func randString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=")[:n]
}

func openBrowser(u string) {
	cmd := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
		return
	}
	exec.Command(cmd, u).Start()
}
