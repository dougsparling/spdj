package main

import (
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Output is terse on purpose: an agent reads every line.

func cmdStart(c *Client, args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	vibe := fs.String("vibe", "", "short description of the vibe (playlist name + description)")
	seed := fs.String("seed", "current", `"Artist - Title", a spotify:track: URI, or "current"`)
	fs.Parse(args)
	if *vibe == "" {
		return fmt.Errorf("--vibe required")
	}

	var track *Track
	progress := 0
	if *seed == "current" {
		p, err := c.player()
		if err != nil {
			return err
		}
		if p.Item == nil {
			return fmt.Errorf(`nothing playing; pass --seed "Artist - Title"`)
		}
		track, progress = p.Item, p.ProgressMS
	} else {
		t, cands, err := c.resolve(*seed)
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("seed not found: %q%s", *seed, candidates(cands))
		}
		track = t
	}

	name := time.Now().Format("2006-01-02") + noteSep + *vibe
	var pl struct {
		ID string `json:"id"`
	}
	if err := c.call("POST", "/me/playlists", map[string]any{"name": name, "public": false, "description": *vibe}, &pl); err != nil {
		return err
	}
	if err := c.addTracks(pl.ID, []string{track.URI}, -1); err != nil {
		return err
	}
	// appended picks only play in order with shuffle off
	if err := c.call("PUT", "/me/player/shuffle?state=false", nil, nil); err != nil {
		return err
	}
	if err := c.call("PUT", "/me/player/play", map[string]any{
		"context_uri": "spotify:playlist:" + pl.ID,
		"offset":      map[string]int{"position": 0},
		"position_ms": progress,
	}, nil); err != nil {
		return err
	}
	fmt.Printf("STARTED %q now=%q queued=0\n", name, track.String())
	return nil
}

func cmdAdd(c *Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: spdj add "Artist - Title" ...`)
	}
	id, p, err := c.sessionPlaylist()
	if err != nil {
		return err
	}
	var uris []string
	for _, q := range args {
		t, cands, err := c.resolve(q)
		if err != nil {
			return err
		}
		if t == nil {
			fmt.Printf("NOT_FOUND %q%s\n", q, candidates(cands))
			continue
		}
		uris = append(uris, t.URI)
		fmt.Printf("ADDED %q (%s)\n", t.String(), t.Album.Name)
	}
	if len(uris) > 0 {
		if err := c.addTracks(id, uris, -1); err != nil {
			return err
		}
	}
	tracks, err := c.playlistTracks(id)
	if err != nil {
		return err
	}
	fmt.Printf("queued=%d\n", queued(tracks, p.Item))
	return nil
}

func cmdNote(c *Client, args []string) error {
	id, _, err := c.sessionPlaylist()
	if err != nil {
		return err
	}
	return c.addNote(id, strings.Join(args, " "))
}

func cmdStatus(c *Client) error {
	id, p, err := c.sessionPlaylist()
	if err != nil {
		return err
	}
	var pl struct {
		Name string `json:"name"`
	}
	if err := c.call("GET", "/playlists/"+id+"?fields=name", nil, &pl); err != nil {
		return err
	}
	desc, err := c.description(id)
	if err != nil {
		return err
	}
	tracks, err := c.playlistTracks(id)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n%s\n", pl.Name, desc)
	cur := currentIndex(tracks, p.Item)
	for i, t := range tracks {
		switch {
		case i == cur:
			state := "playing"
			if !p.IsPlaying {
				state = "paused"
			}
			fmt.Printf("> %d. %s  [%s %s/%s]\n", i+1, t, state, mmss(p.ProgressMS), mmss(t.DurationMS))
		case cur >= 0 && i > cur:
			fmt.Printf("  %d. %s  [queued]\n", i+1, t)
		default:
			fmt.Printf("  %d. %s\n", i+1, t)
		}
	}
	return nil
}

const (
	pollEvery = 3 * time.Second
	// a track that changes with less than this left counts as played through, not skipped
	playedSlack = 8 * time.Second
	idleLimit   = 30 * time.Minute
)

// cmdWait blocks until the track changes, does the playlist bookkeeping, and prints one line:
//
//	NEXT       played through; agent should top the queue back up
//	SKIPPED    listener skipped; skipped track and stale picks removed
//	MANUAL     listener played something else; it was pulled into the playlist and stale picks removed
//	JUMP       listener moved around inside the playlist
//	IDLE       nothing playing for a long time
func cmdWait(c *Client) error {
	id, p, err := c.sessionPlaylist()
	if err != nil {
		return err
	}
	sessionURI := "spotify:playlist:" + id
	tracks, err := c.playlistTracks(id)
	if err != nil {
		return err
	}
	if p.Item == nil {
		return fmt.Errorf("nothing playing")
	}
	if n := queued(tracks, p.Item); n < 2 {
		fmt.Printf("NEED_TRACK now=%q queued=%d\n", p.Item.String(), n)
		return nil
	}

	prev, lastProgress := p.Item, p.ProgressMS
	idleSince := time.Now()
	for {
		time.Sleep(pollEvery)
		p, err := c.player()
		if err != nil {
			return err
		}
		if p.Item == nil || !p.IsPlaying {
			if time.Since(idleSince) > idleLimit {
				fmt.Println("IDLE nothing playing for 30m")
				return nil
			}
			continue
		}
		idleSince = time.Now()
		if p.Item.URI == prev.URI {
			lastProgress = p.ProgressMS
			continue
		}
		return trackChanged(c, id, sessionURI, prev, lastProgress, p)
	}
}

func trackChanged(c *Client, id, sessionURI string, prev *Track, lastProgress int, p *Player) error {
	tracks, err := c.playlistTracks(id)
	if err != nil {
		return err
	}
	prevIdx := lastIndex(tracks, prev.URI)
	newIdx := currentIndex(tracks, p.Item)
	now := p.Item.String()

	if p.contextURI() != sessionURI || newIdx < 0 {
		// pull the listener's pick into the setlist right after the last track and resume the playlist on it
		var stale []string
		pos := len(tracks)
		if prevIdx >= 0 {
			stale, pos = uris(tracks[prevIdx+1:]), prevIdx+1
		}
		if err := c.removeTracks(id, stale); err != nil {
			return err
		}
		if err := c.addTracks(id, []string{p.Item.URI}, pos); err != nil {
			return err
		}
		if err := c.call("PUT", "/me/player/play", map[string]any{
			"context_uri": sessionURI,
			"offset":      map[string]int{"position": pos},
			"position_ms": p.ProgressMS + 1000,
		}, nil); err != nil {
			return err
		}
		c.addNote(id, "+"+p.Item.Name+" (their pick)")
		fmt.Printf("MANUAL now=%q flushed=%d queued=0\n", now, len(stale))
		return nil
	}

	remaining := time.Duration(prev.DurationMS-lastProgress) * time.Millisecond
	switch {
	case prevIdx >= 0 && newIdx != prevIdx+1:
		fmt.Printf("JUMP now=%q queued=%d\n", now, len(tracks)-newIdx-1)
	case remaining > pollEvery+playedSlack:
		stale := uris(tracks[newIdx+1:])
		if err := c.removeTracks(id, append(stale, prev.URI)); err != nil {
			return err
		}
		c.addNote(id, "-"+prev.Name+" (skip)")
		fmt.Printf("SKIPPED %q at %s/%s now=%q flushed=%d queued=0\n",
			prev.String(), mmss(lastProgress), mmss(prev.DurationMS), now, len(stale))
	default:
		fmt.Printf("NEXT now=%q queued=%d\n", now, len(tracks)-newIdx-1)
	}
	return nil
}

// currentIndex finds the playing track in the setlist; with repeats, the latest occurrence wins.
func currentIndex(tracks []Track, cur *Track) int {
	if cur == nil {
		return -1
	}
	return lastIndex(tracks, cur.URI)
}

func lastIndex(tracks []Track, uri string) int {
	for i := len(tracks) - 1; i >= 0; i-- {
		if tracks[i].URI == uri {
			return i
		}
	}
	return -1
}

func queued(tracks []Track, cur *Track) int {
	i := currentIndex(tracks, cur)
	if i < 0 {
		return 0
	}
	return len(tracks) - i - 1
}

func uris(tracks []Track) []string {
	out := make([]string, 0, len(tracks))
	for _, t := range tracks {
		if !slices.Contains(out, t.URI) {
			out = append(out, t.URI)
		}
	}
	return out
}

func candidates(ts []Track) string {
	if len(ts) == 0 {
		return ""
	}
	var names []string
	for _, t := range ts[:min(3, len(ts))] {
		names = append(names, t.String())
	}
	return " candidates: " + strings.Join(names, " | ")
}

func mmss(ms int) string { return fmt.Sprintf("%d:%02d", ms/60000, ms/1000%60) }
