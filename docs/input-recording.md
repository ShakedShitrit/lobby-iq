# Input recording

LobbyIQ can record what you do on your controller during every match. You can
then watch the saved replay with your inputs shown beside it, so you can see
exactly what you pressed and when.

## Turning it on

In `config.yaml`:

```yaml
input_recording:
  enabled: true
  keep: 20          # temporary recordings kept; older ones are deleted
  dir: ""           # "" = a "recordings" folder next to players.json
  replays_dir: ""   # "" = Documents\My Games\Rocket League\TAGame\Demos
  devices: auto     # or any of: xinput, hid, kbm
  poll_hz: 250
  open_viewer_on_replay: true
```

You can also run `lobby-iq --record-inputs` for a single session.

- **When it records:** it starts when a match starts and saves when the match
  ends. While it's recording, the window shows a red **● REC**. Leaving a match
  early still saves what was recorded, marked incomplete.
- **Which recordings are kept:** see
  [Temporary and kept recordings](#temporary-and-kept-recordings) below.
- **Which devices it reads:**

  | Setting | Device |
  | --- | --- |
  | `xinput` | Xbox pads, and any pad that Steam Input or DS4Windows presents as one |
  | `hid` | a DualShock 4 or DualSense connected directly, over USB or Bluetooth |
  | `kbm` | keyboard and mouse buttons |

- **Keyboard privacy:** the keyboard is only read while Rocket League is the
  window in front. As soon as focus leaves the game, every key reads as
  released, so nothing typed in another window is ever recorded. Mouse
  movement isn't recorded.

To check that a controller is detected before you play:

```
LOBBYIQ_LIVE_INPUT=1 go test -run TestLiveSources -v ./internal/inputrec
```

## Temporary and kept recordings

Every match is recorded, but only some are worth keeping. Rocket League
already asks you to decide which: you save a replay of the ones you care
about. LobbyIQ follows that decision.

| Folder | What's in it | How long it stays |
| --- | --- | --- |
| `recordings/tmp/` | every match, as it ends | only the newest `keep` (20 by default) |
| `recordings/saved/` | matches whose replay you saved | for good; never deleted automatically |

- **How a recording gets kept:** LobbyIQ watches the folder Rocket League saves
  replays to. Every replay's header carries the match ID it was recorded from,
  so when one appears that matches a temporary recording, that recording moves
  to `saved/`. The order doesn't matter: if the replay is already saved when
  the match's recording is written, the recording goes straight to `saved/`.
- **What a kept recording records:** the replay's file and the name you gave
  it, as `replay_file` and `replay_name`.
- **Saving while LobbyIQ is closed:** works too. Matching replays are found the
  next time LobbyIQ, `inputs view` or `inputs list` runs, as long as the
  recording is still in `tmp/`.
- **Keeping one by hand:** press **Keep** in the viewer to keep a recording
  without saving its replay.
- **Older recordings:** ones from before this layout, saved directly in
  `recordings/`, are moved into `tmp/` and kept the same way.

In the viewer and in `lobby-iq inputs list`, kept recordings are marked ★.

## Watching a recording

### Automatically

Open the match's replay from Match History in Rocket League. The game reports
a replay under the original match's ID, so LobbyIQ recognises the match and
opens its recording in the viewer, already following along.

- **Locking on:** the viewer locks on at the first moment it recognises. That
  can be a kickoff, a goal, or a ball touch. From then on it follows pauses,
  speed changes and seeking in the replay.
- **What it doesn't do:** a replay is never recorded as a match, and it doesn't
  count towards the session tally or the GAMES column.
- **Turning it off:** set `open_viewer_on_replay: false` to stop the window
  opening on its own. It can take focus from the game as it opens. With the
  viewer already open, it switches to the replay's recording either way.

The `lobby-iq inputs view` command does the same following on its own Stats
API connection. With the main LobbyIQ window running, use its **Inputs**
button instead, which shares that window's connection.

### By hand

1. Open the replay, and in the viewer pick **Whole match** under Playback.
2. Press **Sync at GO** the moment the replay shows "GO" at the first kickoff.
   You can also press **F8** without leaving the game; **F9** pauses and
   resumes.

The whole match then plays through, with goal replays cut out. Pressing Sync
at any later GO snaps to that kickoff and corrects any drift.

**One kickoff at a time** plays a single kickoff from its GO and then queues
up the next one.

If the replay is played slower, set the matching speed in the viewer. The
**±10 ms** and **±100 ms** buttons fine-tune the alignment.

Fyne can't keep a window on top of the game. Put the viewer on a second
monitor, or run Rocket League windowed or borderless and place the viewer
beside it.

## How syncing works

A saved replay leaves out the goal replays (and pauses) that happen live. So
the recording can't simply be played from the start alongside the replay: it
would fall further behind with every goal.

- **Continuous time:** the recording's time with those stretches cut out. The
  cuts are worked out from the recorded `GoalReplayStart`/`GoalReplayEnd` and
  `MatchPaused`/`MatchUnpaused` events. **Whole match** mode plays in
  continuous time.
- **Kickoffs:** from "GO" until the next goal, game time and real time run at
  the same rate, and a replay plays back at that same rate. That is why each
  recording is also cut into **segments**, one per kickoff, each starting at
  its own "GO".
- **The replay's position:** while a replay plays, the Stats API reports it in
  every `UpdateState` (`Game.Elapsed`, in seconds). It arrives only
  `PacketSendRate` times a second, 2 by default, so the viewer carries it
  forward between updates.
- **Matching events:** the game also fires the same events in a replay as it
  did live, on the same tick. The viewer matches these against the recording:

  | Event in the replay | Matched to the recorded event with |
  | --- | --- |
  | `RoundStarted` | the same game clock |
  | `GoalScored` | the same `GoalSpeed` |
  | `BallHit` | the same ball location |

  Each match ties the replay's clock to the recording exactly. Between matches
  the viewer plays on in continuous time.

The start of each segment comes from the Stats API's `RoundStarted` event,
which arrives 4.0 s after `CountdownBegin` in real matches. If `RoundStarted`
doesn't arrive, the recorder falls back to an estimate. The segment's `anchor`
field records which method was used:

| `anchor` | Where the segment's start comes from |
| --- | --- |
| `round_started` | the game's own kickoff event |
| `countdown` | 4 seconds after `CountdownBegin` |
| `clock` | 1 second before the clock was first seen moving |
| `unpaused` | the moment a pause ended |
| `none` | no kickoff was seen at all; the whole match is one segment |

To see what the game actually sends, run `lobby-iq --dump-events events.jsonl`
during a match. Every raw Stats API event is appended to that file, with the
time it arrived.

## File format

Each match is saved as one JSON file:
`<UTC yyyymmdd-hhmmss>_<arena>_<guid8>.lirec.json`. Times in the file are
milliseconds since recording started. Samples and events are timed on the
same monotonic clock.

```json
{
  "version": 1,
  "match_guid": "5002996C11F184800B8C78A4C92835D5",
  "arena": "Stadium_P",
  "started_at": "2026-11-19T20:15:03Z",
  "complete": true,
  "replay_file": "AEDC3B0A4CAF7D899F00E389E247EAEE.replay",
  "replay_name": "test2",
  "poll_hz": 250,
  "duration_ms": 371200,
  "devices": [{"id": 0, "kind": "dualsense", "name": "DualSense Wireless Controller"}],
  "segments": [
    {"index": 0, "anchor_ms": 4120, "end_ms": 41230, "anchor": "round_started",
     "clock_at_anchor": 300, "overtime": false, "ended_by": "goal", "goal_team": 1}
  ],
  "phases": [{"at_ms": 0, "phase": "pregame"}, {"at_ms": 1100, "phase": "countdown"}],
  "events": [{"at_ms": 4120, "name": "RoundStarted", "data": {"MatchGuid": "..."}}],
  "samples": [[4380, 0, 260, 0, 4096, -1200, 300, 0, 0, 0, 255]]
}
```

### Sample rows

Each sample is a positional array:

```
[at_ms, segment, t_ms, device, buttons, lx, ly, rx, ry, lt, rt, keys?]
```

- **`segment` and `t_ms`:** `segment` is the index of the segment the sample
  falls in, or `-1` outside any segment (countdowns, goal replays). `t_ms` is
  the sample's offset from that segment's start.
- **When samples are taken:** only when a device's state changes. A sample
  holds until the next sample for the same device. To find what was held at a
  given time, take that device's last sample at or before it.
- **`buttons`:** XInput's bit layout, as listed below.

  | Bit | Button | Bit | Button |
  | --- | --- | --- | --- |
  | `0x0001` | d-pad up | `0x0100` | LB |
  | `0x0002` | d-pad down | `0x0200` | RB |
  | `0x0004` | d-pad left | `0x0400` | Guide/PS |
  | `0x0008` | d-pad right | `0x0800` | touchpad |
  | `0x0010` | Start | `0x1000` | A |
  | `0x0020` | Back | `0x2000` | B |
  | `0x0040` | left stick click | `0x4000` | X |
  | `0x0080` | right stick click | `0x8000` | Y |

  PlayStation buttons are mapped to the Xbox button in the same position:
  cross is A, circle is B, square is X, triangle is Y.
- **Sticks:** −32768 to 32767, with up as positive.
- **Triggers:** 0 to 255.
- **`keys`:** the virtual-key codes being held, in ascending order. Only the
  keyboard device has it, and only when at least one key is down.

### Segment fields

- **`ended_by`:** one of the following.

  | Value | Meaning |
  | --- | --- |
  | `goal` | a goal was scored |
  | `match_end` | the match ended |
  | `paused` | the match was paused |
  | `countdown` | a new kickoff began, but the stoppage before it wasn't reported |
  | `recording_end` | the recording stopped |

- **`goal_team`:** the scoring team's `TeamNum`, where 0 is Blue and 1 is
  Orange.
