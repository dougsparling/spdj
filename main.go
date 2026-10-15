package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

const usage = `spdj: agent-driven DJing on Spotify

  spdj auth [--client-id ID]      log in; without a client ID, walks you through creating one
  spdj genius-token [FILE]        optional: save a Genius API token (prompts if no FILE) so new
                                  tracks come with about= text
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
  1. spdj start (or spdj resume), seeded from what's playing or a track that fits the vibe.
     Keep --vibe to a few words (it becomes the playlist name); put detail in spdj note.
  2. spdj add two picks. Keep exactly 2 queued (add refuses more): deeper picks go stale
     as soon as the listener skips or gives feedback.
  3. Run spdj wait in the background. It exits with one line when the track changes:
       NEXT        played through: mild approval; add 1
       SKIPPED     listener skipped it (removed, stale picks flushed): change direction; add 2
       JUMP        listener moved within the playlist; top up to 2
       NEED_TRACK  fewer than 2 queued; add picks
       ENDED       listener took over (played something outside the session); stop the loop
       IDLE        nothing played for 30m; stop
     Any event may end with liked=new: the listener liked the previous track while it played,
     the strongest positive signal you'll get. ADDED lines marked [liked] are known favourites.
     add refuses songs already in the set (DUPLICATE).
  4. Top back up to 2 and wait again. Record anything the listener says with spdj note.
  When a new track starts, give the listener a line or two of flavour: who it is, where it
  comes from, why it fits (or turns) the set. Draw on the about= line if present, but
  paraphrase. The listener is mostly passive; this is the DJ's patter.
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
	case "genius-token":
		err = cmdGeniusToken(args)
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
	cfg.RedirectURI = *redirect
	if cfg.ClientID == "" {
		id, err := walkthrough(cfg.RedirectURI)
		if err != nil {
			return err
		}
		cfg.ClientID = id
	}
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
	if me.Product != "premium" {
		fmt.Println("warning: Spotify only allows playback control (and developer apps) for Premium accounts; spdj won't work without it")
	}
	return nil
}

var clientIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// walkthrough guides the user through creating their own Spotify app, which has to happen in
// Spotify's dashboard: there is no API for it.
func walkthrough(redirect string) (string, error) {
	const dashboard = "https://developer.spotify.com/dashboard/create"
	fmt.Printf(`spdj needs its own Spotify app (a client ID). This is a one-time, ~2 minute step.
Requires Spotify Premium: Spotify only lets Premium accounts own developer apps or control playback.

  1. Sign in at %s
  2. Fill in:
       App name:      spdj   (anything)
       Description:   spdj   (anything)
       Redirect URI:  %s   (click Add)
       APIs used:     Web API
  3. Accept the terms, click Save, and copy the Client ID from the app's page.
     (The client secret isn't needed.)

`, dashboard, redirect)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// no terminal to prompt on (e.g. run by an agent)
		return "", fmt.Errorf("then run: spdj auth --client-id CLIENT_ID")
	}
	openBrowser(dashboard)
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("Client ID: ")
		if !in.Scan() {
			return "", fmt.Errorf("no client ID entered; run: spdj auth --client-id CLIENT_ID")
		}
		id := strings.ToLower(strings.TrimSpace(in.Text()))
		if clientIDPattern.MatchString(id) {
			return id, nil
		}
		fmt.Println("That doesn't look like a client ID (32 letters and digits). Try again.")
	}
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
