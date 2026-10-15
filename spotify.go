package main

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode"
)

type Track struct {
	URI        string `json:"uri"`
	Name       string `json:"name"`
	DurationMS int    `json:"duration_ms"`
	IsPlayable *bool  `json:"is_playable"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
}

func (t Track) String() string {
	if len(t.Artists) == 0 {
		return t.Name
	}
	return t.Name + " — " + t.Artists[0].Name
}

type Player struct {
	IsPlaying    bool   `json:"is_playing"`
	ProgressMS   int    `json:"progress_ms"`
	ShuffleState bool   `json:"shuffle_state"`
	Item         *Track `json:"item"`
	Context      *struct {
		URI string `json:"uri"`
	} `json:"context"`
}

func (p *Player) contextURI() string {
	if p.Context == nil {
		return ""
	}
	return p.Context.URI
}

// player returns an empty Player (Item == nil) when nothing is active.
func (c *Client) player() (*Player, error) {
	var p Player
	return &p, c.call("GET", "/me/player?market=from_token", nil, &p)
}

// sessionPlaylist is the playlist currently playing: the DJ session lives there.
func (c *Client) sessionPlaylist() (string, *Player, error) {
	p, err := c.player()
	if err != nil {
		return "", nil, err
	}
	id, ok := strings.CutPrefix(p.contextURI(), "spotify:playlist:")
	if !ok {
		return "", p, fmt.Errorf("not playing a playlist; run: spdj start")
	}
	return id, p, nil
}

func (c *Client) playlistTracks(id string) ([]Track, error) {
	var tracks []Track
	next := "/playlists/" + id + "/items?limit=100&market=from_token&fields=next,items(item(uri,name,duration_ms,artists(name)))"
	for next != "" {
		var page struct {
			Next  string `json:"next"`
			Items []struct {
				Item *Track `json:"item"`
			} `json:"items"`
		}
		if err := c.call("GET", next, nil, &page); err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.Item != nil {
				tracks = append(tracks, *it.Item)
			}
		}
		next = page.Next
	}
	return tracks, nil
}

func (c *Client) addTracks(id string, uris []string, position int) error {
	body := map[string]any{"uris": uris}
	if position >= 0 {
		body["position"] = position
	}
	return c.call("POST", "/playlists/"+id+"/items", body, nil)
}

// removeTracks removes every occurrence of each URI. Passing a snapshot_id makes Spotify silently ignore it, so don't.
func (c *Client) removeTracks(id string, uris []string) error {
	if len(uris) == 0 {
		return nil
	}
	items := make([]map[string]string, len(uris))
	for i, u := range uris {
		items[i] = map[string]string{"uri": u}
	}
	return c.call("DELETE", "/playlists/"+id+"/items", map[string]any{"items": items}, nil)
}

// The playlist description doubles as the session's feedback log: "vibe · note · note ...".
// Spotify rejects newlines and caps it somewhere between 501 and 600 chars.
const (
	noteSep    = " · "
	maxDescLen = 500
)

func (c *Client) description(id string) (string, error) {
	var pl struct {
		Description string `json:"description"`
	}
	err := c.call("GET", "/playlists/"+id+"?fields=description", nil, &pl)
	return html.UnescapeString(pl.Description), err
}

// addNote appends to the description, dropping the oldest notes (never the vibe) to fit.
func (c *Client) addNote(id, note string) error {
	desc, err := c.description(id)
	if err != nil {
		return err
	}
	note = strings.Join(strings.Fields(note), " ")
	parts := append(strings.Split(desc, noteSep), note)
	for len(strings.Join(parts, noteSep)) > maxDescLen && len(parts) > 2 {
		parts = append(parts[:1], parts[2:]...)
	}
	d := strings.Join(parts, noteSep)
	if len(d) > maxDescLen {
		d = d[:maxDescLen]
	}
	return c.call("PUT", "/playlists/"+id, map[string]string{"description": d}, nil)
}

// resolve turns "Artist - Title" (or a spotify:track: URI) into a playable track.
// On a miss it returns nearby candidates so the caller can retry with a better query.
func (c *Client) resolve(q string) (*Track, []Track, error) {
	if strings.HasPrefix(q, "spotify:track:") {
		var t Track
		err := c.call("GET", "/tracks/"+strings.TrimPrefix(q, "spotify:track:")+"?market=from_token", nil, &t)
		return &t, nil, err
	}
	artist, title, ok := splitQuery(q)
	if !ok {
		res, err := c.search(q, 5)
		for _, t := range res {
			if playable(t) {
				return &t, nil, err
			}
		}
		return nil, res, err
	}
	res, err := c.search(fmt.Sprintf(`track:"%s" artist:"%s"`, title, artist), 10)
	if err != nil {
		return nil, nil, err
	}
	if t := bestMatch(res, artist, title); t != nil {
		return t, nil, nil
	}
	// field filters miss on punctuation differences; fall back to free text
	res, err = c.search(title+" "+artist, 10)
	if err != nil {
		return nil, nil, err
	}
	if t := bestMatch(res, artist, title); t != nil {
		return t, nil, nil
	}
	return nil, res, nil
}

func (c *Client) search(q string, limit int) ([]Track, error) {
	var r struct {
		Tracks struct {
			Items []Track `json:"items"`
		} `json:"tracks"`
	}
	path := fmt.Sprintf("/search?type=track&market=from_token&limit=%d&q=%s", limit, url.QueryEscape(q))
	return r.Tracks.Items, c.call("GET", path, nil, &r)
}

func splitQuery(q string) (artist, title string, ok bool) {
	for _, sep := range []string{" - ", " — ", " – "} {
		if a, t, found := strings.Cut(q, sep); found {
			return strings.TrimSpace(a), strings.TrimSpace(t), true
		}
	}
	return "", "", false
}

// bestMatch requires the artist to match, then prefers an exact title, then a non-live version.
func bestMatch(res []Track, artist, title string) *Track {
	na, nt := norm(artist), norm(title)
	var best *Track
	bestScore := -1
	for i, t := range res {
		if !playable(t) || !artistMatches(t, na) {
			continue
		}
		score := 0
		tn := norm(t.Name)
		switch {
		case tn == nt:
			score = 3
		case strings.HasPrefix(tn, nt) || strings.HasPrefix(nt, tn):
			score = 2
		case strings.Contains(tn, nt):
			score = 1
		default:
			continue
		}
		if !strings.Contains(nt, "live") && strings.Contains(strings.ToLower(t.Name), "live") {
			score--
		}
		if score > bestScore {
			best, bestScore = &res[i], score
		}
	}
	return best
}

func artistMatches(t Track, na string) bool {
	for _, a := range t.Artists {
		n := norm(a.Name)
		if n == "" || na == "" {
			continue
		}
		if n == na || strings.Contains(n, na) || strings.Contains(na, n) {
			return true
		}
	}
	return false
}

func playable(t Track) bool { return t.IsPlayable == nil || *t.IsPlayable }

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
