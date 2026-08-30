# Discord Rich Presence

LobbyIQ can publish your live match to Discord as a Rich Presence card:

```
Playing Rocket League
2v2 · Blue 3 - 1 Orange
540 pts · 2G 1A 3S 4Sh · Session +2
02:14 elapsed                          [View on Tracker]
```

It's off until you give it a Discord application client ID. That takes about
two minutes, once.

## 1. Create the Discord application

1. Go to <https://discord.com/developers/applications> and click **New
   Application**.
2. **Name it `Rocket League`.** This matters: Discord takes the "Playing X"
   line from the *application's* name, not from anything LobbyIQ sends. Name
   it something else and your status reads "Playing something else".
3. On **General Information**, copy the **Application ID** (also called the
   client ID). It's an 18-19 digit number.

## 2. Upload the art assets

There are two ways to do this, and you only need one.

### Option A: point at image URLs from config (nothing to upload)

Discord accepts an `https://` URL anywhere an asset key goes, and hosts a copy
itself. So the images can be set entirely from `config.yaml`:

```yaml
discord_assets:
  logo: "https://example.com/rocket-league.png"
  blue: "https://example.com/blue.png"
  orange: "https://example.com/orange.png"
```

Any field left out falls back to the uploaded-asset key below, so the two
options mix freely. URLs also support GIF and animated WebP, which uploaded
assets don't.

The catch is that the images have to be reachable by URL from the public
internet - a local file path won't do - and support for a URL in the *small*
image has historically been less reliable than in the large one. If a small
badge doesn't appear, upload that one and leave the large one as a URL.

### Option B: upload them to the application

Under **Rich Presence → Art Assets**, upload images with these exact keys. The
key is taken from the filename you upload, minus the extension, so uploading
`team_blue.png` gives you the key `team_blue` with nothing else to do.

| Key              | Shown as              | Image                                |
| ---------------- | --------------------- | ------------------------------------ |
| `rocket_league`  | the large icon        | the Rocket League logo — see below   |
| `team_blue`      | small badge, on Blue  | `assets/discord/team_blue.png`       |
| `team_orange`    | small badge, on Orange| `assets/discord/team_orange.png`     |

The two team badges ship with this repo, already 512x512 with a transparent
background. `assets/discord/make-badges.ps1` is what drew them - rerun it with
an output directory to change the colors.

For `rocket_league`, any square Rocket League logo works. Discord hosts the
image itself, so it needs to be a file on disk, not a link:

- The game's own icon: right-click Rocket League in Steam → **Manage → Browse
  local files**, and the .exe's icon can be pulled out with any icon
  extractor. In the Epic launcher the icon lives under the install directory
  too.
- The Rocket League press kit at <https://www.rocketleague.com/en/news> and the
  logo on <https://en.wikipedia.org/wiki/Rocket_League> are both easy sources
  for a clean, high-resolution version.

Discord requires at least 512x512 and rejects anything smaller, so scale up if
your source is a 256px icon.

Assets can take a few minutes to propagate after upload. A key that doesn't
exist simply isn't rendered, so the text still works if you skip this step or
only upload some of them.

## 3. Point LobbyIQ at it

Add the ID to `config.yaml`:

```yaml
discord_client_id: "123456789012345678"
```

Or pass it per-run:

```
lobby-iq --discord-client-id 123456789012345678
```

Or set `LOBBYIQ_DISCORD_CLIENT_ID` in the environment.

To keep the ID in config but turn the presence off for a run, use
`--no-discord` (or `discord_disabled: true`).

## 4. Optional: show your rank badge

Rocket League's Stats API doesn't report rank at all, so LobbyIQ can't read
it. Set it by hand instead, per gamemode:

```yaml
discord_ranks:
  "1v1": "Diamond II"
  "2v2": "Champion I"
  "3v3": "Champion II"
```

A mode listed here shows that rank's badge as the card's large icon instead of
the logo, and the arena moves into the tooltip beside the rank name. Modes you
leave out keep the logo, so you can set only the playlists you care about.

Spellings are forgiving: `Champion I`, `champion 1`, `champ1` and `c1` all mean
the same thing, as do `gc2` and `Grand Champion II`, or `ssl` and `Supersonic
Legend`. Anything unrecognised is logged as a warning at startup and ignored -
it won't stop the app.

The badges are fetched from tracker.gg's public rank art, which needs nothing
uploaded, and every rank from Unranked to Supersonic Legend has its own image.
One caveat remains: since the game doesn't expose the playlist, a ranked and a
casual 2v2 both show your configured 2v2 rank.

Update the file when you rank up; it's read at startup, so restart LobbyIQ
afterwards.

## What gets published

**In a match** — updated at most once every 5 seconds, which is what Discord's
rate limit allows:

- Gamemode and the score from your team's side, e.g. `2v2 · Blue 3 - 1 Orange`.
  Overtime shows as `2v2 · OT · Blue 3 - 3 Orange`, and a finished match as
  `2v2 · Won 4 - 2`.
- Your stat line: points, goals, assists, saves, plus shots and demos when you
  have any.
- This session's win/loss tally, the same number the app's SESSION row shows.
- Your rank badge as the large icon when `discord_ranks` covers the mode,
  otherwise the logo. Either way the arena is in its tooltip, and your team
  color is the small badge.
- Party size, e.g. "(4 of 4)".
- A **View on Tracker** button linking to your rocketleague.tracker.network
  profile. This is public to anyone who can see your Discord profile; drop the
  `activity.Buttons` block in `internal/presence/presence.go` if you'd rather
  not publish it.

**In the menus** — after 30 seconds without a match update:

- `In the menus`, your session record, and time elapsed since LobbyIQ
  started.

## Troubleshooting

Run with `--log-level debug` and check `lobby-iq.log`.

| Log line | Meaning |
| --- | --- |
| `discord: rich presence disabled ... Invalid Client ID` | The ID isn't a Discord application. Re-copy the Application ID from the developer portal. LobbyIQ stops retrying after this - it can't fix itself - so restart once corrected. |
| `discord: dial failed` (debug only) | Discord isn't running. Harmless; LobbyIQ keeps retrying with backoff. |
| `discord: connected` | Working. If no card shows, check **User Settings → Activity Privacy → Share your detected activities with others** in Discord. |
| nothing about discord at all | No client ID configured, or `--no-discord` is set. |

The status card is not shown to *you* in your own profile popout the way it's
shown to others - to see it as others do, check your name in a server member
list, or ask a friend.

## Notes and limitations

- **Discord doesn't have to be running.** LobbyIQ connects lazily and
  retries with backoff, so starting Discord after LobbyIQ (or restarting it
  mid-session) just works. Nothing is logged above debug level while it's
  absent.
- **Ranked vs. casual is indistinguishable.** Rocket League's Stats API
  reports no playlist, only the roster size, so a ranked 2v2 and a casual 2v2
  both read `2v2`.
- **"You" is inferred** from the player the game camera follows. Goal replays
  follow the scorer instead, so those ticks are ignored. While spectating, the
  card shows the followed player's team and no personal stat line.
- **The presence is cleared** when LobbyIQ exits, and by Discord itself if
  the process dies without a clean shutdown.
