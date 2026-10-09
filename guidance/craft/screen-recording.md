# Screen recording

Software demos, tutorials and walkthroughs show a screen. The viewer must be able to read it, follow the cursor and see each action land with the words that describe it. Facet has no capture tool yet (`capabilities` reports `capture` as unavailable), so there are two routes: edit a recording the operator supplies, or build the screen in the composer with `terminal_scene` and `screenshot_scene`. Ask which one the operator wants; a supplied recording is real, a composer scene is a recreation and is labelled as one.

## Route 1: a supplied recording

Give the operator these settings before they record:

| Setting | Value |
|---|---|
| Size | 4K (or a 2× display) for 1080p delivery, so the picture survives a 2× crop; 1080p is the minimum |
| Frame rate | 60 fps for scrolling and interface motion; 30 for code and terminals |
| Text | editor and terminal fonts 18 to 22 px at 1080p delivery (150 to 175% zoom); dark theme; line numbers on; minimap off; sidebars collapsed |
| Cursor | 1.5 to 2× size with a highlight ring; moved deliberately, resting 0.5 s on a target before clicking; hidden while code is explained |
| Clean screen | notifications off, unrelated tabs and apps closed, bookmarks bar hidden; no personal data, keys, tokens or customer records in frame |

Then in Facet:

1. **Probe:** `media_probe` for size and frame rate. Screen recordings often have a variable frame rate; `source_edit` normalises it to the `target` fps.
2. **Find the parts:** `scene_detect` with `min_scene_length_seconds: 2` splits the recording where the screen changes; `frame_sample` shows each part.
3. **Remove the waiting:** `silence_cutter` removes pauses over 1.5 s; use `mode: "speed_up"` where the screen keeps moving through the silence.
4. **Speed the dull parts** with `video_trimmer` (`operation: "speed"`), never over speech:

| Action | Speed |
|---|---|
| typing boilerplate | 2–3× |
| opening files, switching tabs | 1.5–2× |
| installs and builds | 2–4×, or cut to the start and the end |
| the key action, debugging, reasoning | 1× |

5. **Assemble** the selects with `source_edit` into one format. Jump cuts are normal in screen tutorials; keep them at the end of an action, not in the middle of one.
6. **Voice:** when the recorded voice is weak, script the narration and voice it with a voice tool as `lines`. Cut the recording to its line times, then lay it under the edit with `replacement_audio`, or use it as the composition's narration. Its timing record then drives captions and cut timing.
7. **Vertical:** `source_edit` with `fit: "cover"` and a `focal_point` on the active area crops the landscape recording to 9:16; start a new segment whenever the action moves across the screen. This needs a 4K source.

**Zooms.** Templated cuts do not zoom into video. For a held moment (code at rest, a finished screen), take the frame with `frame_sample` and use it as an `image` cut with `zoom-in`; for zooms that follow live action, use an atelier composition (`guidance/runtimes/composition-modes.md`). Zoom 1.5 to 2× for code, 2 to 2.5× for an interface element, with 0.6 to 0.8 s eased moves, and hold at least 3 s before moving again.

## Route 2: composer scenes

Use these when no recording exists, or for short focused moments: a command sequence, a click path in one screen. Exact fields are in `guidance/runtimes/scene-types.md`; the terminal pattern in depth is in `guidance/vendor/synthetic-screen-recording/SKILL.md`.

**`terminal_scene`** types commands character by character, prints their output, holds on pauses and floats short status pills.

- Use real commands and real output, taken from the product's documentation or a run the operator supplies. Never invent output that makes the product look better than it is.
- Open with at least 2 s of an empty terminal so the viewer registers the window.
- Pace it to the narration: each command starts typing when its narration line starts. Take the times from the `narration_timing` record (line start minus scene start) and fill the gaps with pauses; end with a hold long enough to read the final state.
- Hold at least 0.3 s after each command before its output appears; fire a pill the moment its event completes ("tests passed" right after the last test line).

**`screenshot_scene`** animates a real screenshot: a cursor path, click pulses, typing into a field, highlight boxes, callouts. Coordinates are fractions (0 to 1) of the screenshot.

- The screenshot is real and current, supplied by the operator or taken from the product. Never use an image generator for a product's interface.
- One task a scene, 15 to 30 s. Longer flows become several scenes, one screen each.
- The cursor moves in straight, eased paths and rests before each click; a highlight box comes up as the narration names the element.

**Label it.** Mark every composer-built screen as synthetic in the `scene_plan` and the `asset_manifest`, tell the operator, and say so on screen or in the description wherever a viewer could take it for a real recording.

## Pacing a screen

- Say what to look at before showing it: "look at the second argument", then the highlight or zoom.
- One action a narration sentence; the action lands on its words.
- Code stays on screen long enough to read the lines being discussed; highlight the current line rather than scrolling while talking.
- Follow the active area; never make the viewer search the screen.
- Captions are recommended; they come from the narration's timing record (`guidance/craft/captions.md`).

## Quality checks

- Text in the recording reads at delivery size: check full-size frames from `frame_sample`, and at phone size for vertical.
- No personal data, keys, tokens, email addresses or notifications in any frame.
- The cursor is visible and purposeful; it never wanders or circles.
- Every action lands with its narration line; nothing happens before it is announced.
- Dead air is gone; waits are sped up or cut; speech is never sped.
- Constant frame rate, no stutter on scrolls (`media_probe` on the output).
- Composer screens use real commands, real output and real screenshots, and are labelled as synthetic.
