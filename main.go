package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const usage = `spdj: agent-driven DJing on Spotify

  spdj auth --client-id ID [--redirect URI]
  spdj start --vibe TEXT [--seed current|"Artist - Title"|URI]
                                  new dated playlist seeded with a track, starts playing it
  spdj add "Artist - Title" ...   append picks to the playing session
  spdj wait                       block until the track changes; prints one event line
  spdj status                     setlist, vibe and feedback notes
  spdj note TEXT                  append listener feedback to the playlist description
  spdj api METHOD PATH [JSON]     raw Web API call (debugging)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "auth":
		err = cmdAuth(args)
	case "api":
		err = cmdAPI(args)
	case "start":
		err = cmdStart(mustClient(), args)
	case "add":
		err = cmdAdd(mustClient(), args)
	case "wait":
		err = cmdWait(mustClient())
	case "status":
		err = cmdStatus(mustClient())
	case "note":
		err = cmdNote(mustClient(), args)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func mustClient() *Client {
	cfg, err := loadConfig()
	if err != nil || cfg.RefreshToken == "" {
		fmt.Fprintln(os.Stderr, "not logged in; run: spdj auth --client-id ID")
		os.Exit(1)
	}
	return &Client{cfg: cfg}
}

func cmdAuth(args []string) error {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	clientID := fs.String("client-id", "", "Spotify app client ID")
	redirect := fs.String("redirect", "http://127.0.0.1:8888/callback", "redirect URI registered on the app")
	fs.Parse(args)
	cfg, _ := loadConfig()
	if cfg == nil {
		cfg = &Config{}
	}
	if *clientID != "" {
		cfg.ClientID = *clientID
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("--client-id required")
	}
	cfg.RedirectURI = *redirect
	if err := authorize(cfg); err != nil {
		return err
	}
	var me struct {
		ID      string `json:"id"`
		Product string `json:"product"`
	}
	if err := (&Client{cfg: cfg}).call("GET", "/me", nil, &me); err != nil {
		return err
	}
	fmt.Printf("logged in as %s (%s)\n", me.ID, me.Product)
	return nil
}

func cmdAPI(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: spdj api METHOD PATH [JSON]")
	}
	var body any
	if len(args) > 2 {
		if err := json.Unmarshal([]byte(args[2]), &body); err != nil {
			return err
		}
	}
	var out any
	if err := mustClient().call(args[0], args[1], body, &out); err != nil {
		return err
	}
	if out != nil {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}
