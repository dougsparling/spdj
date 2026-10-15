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
                                  new "SPDJ · vibe" playlist seeded with a track, starts playing it
  spdj sessions                   past sessions, most recent first
  spdj resume [latest|TEXT]       continue a past session from its last track
  spdj add "Artist - Title" ...   append picks to the playing session
  spdj wait                       block until the track changes; prints one event line
  spdj status                     setlist, vibe and feedback notes
  spdj note TEXT                  append listener feedback to the playlist description
  spdj api METHOD PATH [JSON]     raw Web API call (debugging)

Agents: you are the DJ, spdj handles playback.
  1. spdj start (or spdj resume), seeded from what's playing or a track that fits the vibe
  2. spdj add two picks. Keep exactly 2 queued; add refuses more.
  3. Run spdj wait in the background. It exits with one line when the track changes:
       NEXT        played through: mild approval; add 1
       SKIPPED     listener skipped it (removed, stale picks flushed): change direction; add 2
       MANUAL      listener played something themselves (now in the setlist): follow it; add 2
       JUMP        listener moved within the playlist; top up to 2
       NEED_TRACK  fewer than 2 queued; add picks
       IDLE        nothing played for 30m; stop
  4. Top back up to 2 and wait again. Record anything the listener says with spdj note.
  When a new track starts, give the listener a line or two of flavour: who it is, where it comes
  from, and why it fits (or turns) the set. They're mostly passive; this is the DJ's patter.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if a := os.Args[1]; a == "help" || a == "-h" || a == "--help" {
		fmt.Print(usage)
		return
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
	case "sessions":
		err = cmdSessions(mustClient())
	case "resume":
		err = cmdResume(mustClient(), args)
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
