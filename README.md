# spdj

Let an AI agent DJ your Spotify. You put on a track (or describe a vibe), and the agent keeps the music going: it picks what plays next, notices when you skip or like something, and adjusts.

`spdj` is the command-line tool the agent drives. It does the polling and playlist bookkeeping itself and only wakes the agent when a decision is needed. This is much more token efficient than Claude driving the official Spotify connector/MCP directly.

Each session becomes a regular Spotify playlist (e.g. `SPDJ · slow + weird`), so you keep the setlist afterwards and can pick the set back up later.

## Requirements

Unfortunately scripting the Spotify client, reverse engineering its API or driving the public API directly aren't practical for a small hobby app like this. So Premium and some extra setup is required:

- **Spotify Premium.** Spotify only lets Premium accounts control playback, and only Premium accounts can own a developer app.
- **macOS or Linux** for the one-line install, or **Go 1.22+** to build from source (any OS, including Windows).
- Spotify open and playing on some device (desktop app, phone, speaker).

## Setup (for the human)

1. Install the latest release into `~/.local/bin` (make sure it's on your `PATH`):
   ```sh
   mkdir -p ~/.local/bin && curl -fsSL "https://github.com/dougsparling/spdj/releases/latest/download/spdj_$(uname -s)_$(uname -m).tar.gz" | tar -xz -C ~/.local/bin spdj
   ```
   Or build from source with Go:
   ```sh
   go install github.com/dougsparling/spdj@latest
   ```
   Windows binaries are on the [releases page](https://github.com/dougsparling/spdj/releases).
2. Log in:
   ```sh
   spdj auth
   ```
   The first time, this walks you through creating your own Spotify developer app (there's no way to automate it) and asks for its client ID. It then opens a browser tab to approve access and stores the login in `~/.config/spdj/config.json`.

   For reference, the app settings are: any name and description, redirect URI `http://127.0.0.1:8888/callback` (exactly this; Spotify rejects `localhost`), and Web API scope. You don't need the client secret. If you already have a client ID, `spdj auth --client-id ID` skips the walkthrough.

3. *(Optional)* Track backstories from Genius. Create an API client at <https://genius.com/api-clients> (the redirect URI is unused; anything valid works), click **Generate Access Token**, save the token to a file, then you can paste the token:
   ```sh
   spdj genius-token
   ```
   Which will prompt for the token. After that, the DJ agent will have track descriptions at its disposal.

## How it works

- **No local session state.** The `SPDJ · …` playlist that's currently playing *is* the session: its tracks are the setlist, and its description holds the vibe and feedback (`slow + weird · -Torn Maps (skip) · +Tilt (liked)`). The only local file is the login token.
- **`wait` polls** Spotify's player API every 3 seconds. It checks the track's liked state when it starts waiting and again when the track changes. A track that changes with more than ~11 seconds left counts as a skip.
- **Picks are appended to the playlist**, which Spotify plays in order. `spdj start` turns shuffle off for this reason.
- **Anything unexpected ends the session.** If the player leaves the session playlist or plays a track that isn't in it, `wait` reports `ENDED` rather than guessing what the listener meant. To slip in a track of your own, add it to the session playlist in the app, or tell the agent.

## Limitations

- **Session playlists are public.** `spdj` asks Spotify for private playlists, but Spotify's API ignores that. Use **Make private** in the playlist's `⋯` menu in the app if you care.
- **New playlists may not show in the desktop app's sidebar until you restart it.** They're in your library either way, and you can always reach them from the queue ("Next from: …").

- **Spotify reads lag writes by a few seconds.** Two feedback notes written back-to-back can clobber each other, and `spdj sessions` may undercount tracks on a session that was just edited.
- **The description holds about 500 characters.** Older notes get dropped to make room; the vibe is always kept.
- **No recommendations.** Spotify removed its recommendations API for new apps, so picks come entirely from the agent's own knowledge of music.

## For AI agents: how to DJ

You are the DJ. `spdj` handles playback mechanics; you choose the music.

### The loop

1. **Start a session**, or resume a past one (see below). Seed from what's playing, or pick a seed track that fits the listener's vibe:
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

When a new track starts, give the listener a line or two of flavour: who it is, where it comes from, and why it fits (or turns) the set. If the event came with an `about=` line, draw on it, but paraphrase rather than reading it out. The listener is mostly passive, and this is what makes it feel like a DJ rather than a shuffle button.

Keep two tracks queued (`add` refuses picks beyond that with `QUEUE_FULL`). More than that and the picks go stale when the listener gives feedback; fewer and Spotify's autoplay can take over if a track ends while you're thinking.

### Events from `spdj wait`

| Event | What happened | What to do |
|---|---|---|
| `NEXT now="…" queued=1` | Previous track played through. | Add one pick. |
| `SKIPPED "…" at 0:41/4:12 now="…" flushed=N queued=0` | Listener skipped. `spdj` removed the skipped track and your stale picks from the playlist. | Treat it as negative feedback: adjust direction, add two picks. |
| `JUMP now="…" queued=N` | Listener moved around inside the playlist. | Top up to two if needed. |
| `NEED_TRACK now="…" queued=N` | `wait` was called with fewer than two queued. | Add picks, then wait again. |
| `ENDED now="…" …` | The player left the session: the listener played something outside the playlist, or something unexpected happened. | Stop the loop. `spdj resume` picks the session back up later. |
| `IDLE …` | Nothing has played for 30 minutes. | Stop the loop. |

Most of the time the listener is passive, so `NEXT` will be the common case. Treat a played-through track as mild approval, not enthusiasm.

Any event can end with `liked=new`: the listener added the previous track to their Liked Songs while it played. That's the strongest positive signal you'll get, so lean into it. `add` also marks picks that are already in their Liked Songs with `[liked]`.

### Resuming a past session

```sh
spdj sessions                  # past sessions, most recent first, with their feedback notes
spdj resume                    # continue the most recent one from its last track
spdj resume "slow + weird"     # or match by name
```

`resume` prints the session's vibe and feedback notes. Read them, then queue two picks and enter the loop as usual.

### Other commands

```sh
spdj add "Artist - Title" ...  # prints ADDED "…" or NOT_FOUND "…" candidates: …
spdj note "more weird, keep it slow"  # record feedback the listener tells you
spdj status                    # setlist, current position, vibe + feedback notes
```

- **`NOT_FOUND`** means the track isn't on Spotify in the listener's country, or the artist/title is off. Check the candidates, fix the spelling, or pick something else.
- **`note`** writes to the playlist description, which is the session's memory. After losing context mid-session, run `spdj status` first.
- `add`, `wait`, `note` and `status` only act on a playing `SPDJ · …` playlist, so they never edit the listener's own playlists.

### Picking well

- Read the room from feedback, not just genre labels. Skips early in a track (under ~30s) are a stronger signal than late ones.
- Avoid the same artist twice in a row unless asked.
- When the listener says something about the music, `spdj note` it, then adjust.

### Helping the human with setup

`spdj auth` can't prompt when run by an agent. It prints the walkthrough and exits; relay the steps, ask the user for the client ID, then run `spdj auth --client-id ID` (it opens a browser tab for them to approve). `spdj genius-token FILE` also accepts a file with the secret if you have it.

### Debugging

`spdj api METHOD PATH [JSON]` makes a raw, authenticated Web API call and prints the response, e.g. `spdj api GET /me/player`.
