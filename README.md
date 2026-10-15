# spdj

Let an AI agent DJ your Spotify. You put on a track (or describe a vibe), and the agent keeps the music going: it picks what plays next, notices when you skip something, and adjusts.

`spdj` is the command-line tool the agent drives. It was built for long listening sessions, where driving Spotify through a chatty API integration burns a lot of tokens. `spdj` does the polling and playlist bookkeeping itself and only wakes the agent with a one-line event when a decision is needed.

Each session becomes a regular Spotify playlist (e.g. `2026-10-15 · slow + weird`), so you keep the setlist afterwards.

## Requirements

- **Spotify Premium.** Spotify only allows playback control for Premium accounts.
- **A Spotify app (client ID).** About two minutes of setup, covered below.
- **Go 1.22+** to install.
- Spotify open and playing on some device (desktop app, phone, speaker).

## Setup (for the human)

1. Install:
   ```sh
   go install github.com/dougsparling/spdj@latest
   ```
2. Create a Spotify app at <https://developer.spotify.com/dashboard>:
   - **Create app.** Any name and description.
   - **Redirect URI:** `http://127.0.0.1:8888/callback` (exactly this; Spotify rejects `localhost`).
   - **APIs used:** Web API.
   - Copy the **Client ID** from the app's settings. You don't need the client secret.
3. Log in:
   ```sh
   spdj auth --client-id YOUR_CLIENT_ID
   ```
   This opens a browser tab to approve access, then stores the token in `~/.config/spdj/config.json`.

**Why do I need my own app?** Spotify caps apps that haven't been through their (business-only) review at a handful of users, so a shared client ID doesn't scale beyond a few friends. Your own app has no such issue, and the login flow (OAuth with PKCE) never needs a secret.

## For agents: how to DJ

You are the DJ. `spdj` handles playback mechanics; you choose the music.

### The loop

1. **Start a session.** Seed from what's playing, or pick a seed track that fits the listener's vibe:
   ```sh
   spdj start --vibe "slow + weird"                                  # seed = current track
   spdj start --vibe "slow + weird" --seed "Have A Nice Life - Bloodhail"
   ```
2. **Queue two picks.** Always format picks as `"Artist - Title"`:
   ```sh
   spdj add "Scott Walker - Tilt" "Swans - Lunacy"
   ```
3. **Wait for the track to change.** Run this in the background; it blocks (often for minutes) and exits with one line:
   ```sh
   spdj wait
   ```
4. **React to the event**, add picks until `queued=2` again, and go back to step 3.

Keep two tracks queued. More than that and the picks go stale when the listener gives feedback; fewer and Spotify's autoplay can take over if a track ends while you're thinking.

### Events from `spdj wait`

| Event | What happened | What to do |
|---|---|---|
| `NEXT now="…" queued=1` | Previous track played through. | Add one pick. |
| `SKIPPED "…" at 0:41/4:12 now="…" flushed=N queued=0` | Listener skipped. `spdj` removed the skipped track and your stale picks from the playlist. | Treat it as negative feedback: adjust direction, add two picks. |
| `MANUAL now="…" flushed=N queued=0` | Listener played something themselves. `spdj` pulled it into the session playlist and removed stale picks. | Strong positive signal about where they want to go. Add two picks that follow from it. |
| `JUMP now="…" queued=N` | Listener moved around inside the playlist. | Top up to two if needed. |
| `NEED_TRACK now="…" queued=N` | `wait` was called with fewer than two queued. | Add picks, then wait again. |
| `IDLE …` | Nothing has played for 30 minutes. | Stop the loop. |

Most of the time the listener is passive, so `NEXT` will be the common case. Treat a played-through track as mild approval, not enthusiasm.

### Other commands

```sh
spdj add "Artist - Title" ...  # prints ADDED "…" or NOT_FOUND "…" candidates: …
spdj note "more weird, keep it slow"  # record feedback the listener tells you
spdj status                    # setlist, current position, vibe + feedback notes
```

- **`NOT_FOUND`** means the track isn't on Spotify in the listener's country, or the artist/title is off. Check the candidates, fix the spelling, or pick something else.
- **`note`** writes to the playlist description, which is the session's memory. When resuming a session (or after losing context), run `spdj status` first.

### Picking well

- Read the room from feedback, not just genre labels. Skips early in a track (under ~30s) are a stronger signal than late ones.
- Avoid the same artist twice in a row unless asked.
- When the listener says something about the music, `spdj note` it, then adjust.

## How it works

- **No local session state.** The playlist that's currently playing *is* the session: its tracks are the setlist, and its description holds the vibe and feedback (`slow + weird · -Torn Maps (skip) · +Tilt (their pick)`). The only local file is the login token.
- **`wait` polls** Spotify's player API every 3 seconds. A track that changes with more than ~11 seconds left counts as a skip.
- **Picks are appended to the playlist**, which Spotify plays in order. `spdj start` turns shuffle off for this reason.
- **Manual picks get steered back:** the track is inserted into the playlist and playback restarts on it from within the playlist at the same position (expect a brief hiccup).

## Limitations

- **Switching away hijacks your music.** If you put on a different album or playlist on purpose, `wait` treats it as a manual pick and pulls it into the session. Stop the agent's loop first.
- **Feedback notes are eventually consistent.** Spotify's playlist description reads lag writes by a few seconds, so two notes written back-to-back can clobber each other.
- **The description holds about 500 characters.** Older notes get dropped to make room; the vibe is always kept.
- **No recommendations.** Spotify removed its recommendations API for new apps, so picks come entirely from the agent's own knowledge of music.

## Debugging

`spdj api METHOD PATH [JSON]` makes a raw, authenticated Web API call and prints the response, e.g. `spdj api GET /me/player`.
