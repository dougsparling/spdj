package main

import "testing"

func track(uri, name, artist string) *Track {
	t := &Track{URI: uri, Name: name}
	t.Artists = append(t.Artists, struct {
		Name string `json:"name"`
	}{artist})
	return t
}

func TestSameSong(t *testing.T) {
	jesse := track("spotify:track:a", "Jesse", "Scott Walker")
	cases := []struct {
		other *Track
		want  bool
	}{
		{track("spotify:track:a", "Jesse", "Scott Walker"), true},     // same ID
		{track("spotify:track:video", "Jesse", "Scott Walker"), true}, // video version
		{track("spotify:track:b", "Jesse - 2006 Remaster", "Scott Walker"), true},
		{track("spotify:track:c", "Jesse", "Jesse Walker"), false}, // different artist
		{track("spotify:track:d", "Clara", "Scott Walker"), false},
	}
	for _, c := range cases {
		if got := sameSong(jesse, c.other); got != c.want {
			t.Errorf("sameSong(Jesse, %s) = %v, want %v", c.other, got, c.want)
		}
	}
}
