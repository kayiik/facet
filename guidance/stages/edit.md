# Stage: edit

**Run by:** the editor, with the sound designer. `plan_check` checks the cut; the critic reviews it.
**Records:** `edit_decisions`; the critic's `review` of the cut.

Assets become a timeline: what plays when, how it moves, how layers stack, where captions sit, how voice and music share the space. `edit_decisions` is exactly what `video_compose` renders.

## Inputs

`scene_plan`, `asset_manifest`, `script`, `narration_timing`, and the settings locked at the proposal (or in the brief where the pipeline has no proposal stage).

## Method

1. **Carry the locked settings unchanged:** `render_runtime`, `composition_mode`, `renderer_family`, `delivery_promise`; `theme` is the style id; frame size and fps come from the platform. A change here is a change of plan: ask first.
2. **Map assets to time.** For each scene: its assets and the narration lines it covers. Build cuts on the real narration timing, not the planned durations.
3. **Write the cuts.** Each cut has an `id`, and either a `type` (an Explainer scene type with its fields) or a `source` (a media cut: image or video). Time it with `lines: ["l3","l4"]` to follow those narration lines, or with `in_seconds` and `out_seconds`. Clips take `source_in_seconds`; stills take a camera move in `animation` (`zoom-in`, `zoom-out`, `pan-left`, `pan-right`, `ken-burns`, `parallax`, `static`); the move into the next cut is `transition` (`cut`, `fade`, `slide`, `wipe`, `flip`, `clock-wipe`, `iris`) with `transition_duration`. `CinematicRenderer` takes `scenes` instead of cuts. Each scene type's fields are in `guidance/runtimes/scene-types.md`; unknown fields are refused.
4. **Pace it.** Holds within the style's limits; text long enough to read twice; hero moments hold longest; strong moments are not overcut or covered; something changes on screen every few seconds. Transitions come from the style's set and vary; keep special ones for topic shifts; footage mostly cuts.
5. **Captions.** Decide once whether captions are subtitles for the narration or text that adds meaning (numbers, names, translations); never show both for the same line. In the composition: `captions` with the `timing_path`, `words_per_page` (about 4; 3 for vertical), `highlight_color` and `position`. For burned subtitles on footage or an FFmpeg render: `subtitle_gen` from the `timing_path` (`layout` `horizontal` or `vertical`), then `ffmpeg_caption_burn` after the render.
6. **Sound.** Narration first; music under it with `duck_under_narration: true`, playing only in the chosen sections when the plan says so; sound effects at their seconds (`sfx` cues with `at_seconds` and `gain_db`); the loudness target (−14 LUFS by default for online platforms). Silence is a tool: keep the planned pauses.
7. **Footage.** Prepare selects with `source_edit` (segments into one consistent format), `video_trimmer` (cut, speed) or `silence_cutter` (dead air), then cut from them. Keep the speaker's meaning across every join.
8. **Atelier.** The composition is the edit: `bespoke` with the project-relative `entry` and `composition_id` for Remotion, or `render_runtime` `hyperframes` with the HyperFrames workspace. Stock scene types do not appear in an atelier piece.
9. **Check the cut.** Run `plan_check` on the edit with the narration timing and the delivery promise: full coverage, no gaps or overlaps, variety, slideshow risk, promise fit. Then the critic reviews the cut against the scene plan and the script (`facet-critic`, or yourself from `guidance/stages/review.md`).
10. **Estimate the render.** `estimate` on `video_compose` with the edit. When it reports `prefer_shell`, render with `facet tools run video_compose --input <file>` in the shell.

## The record

`edit_decisions` (`artifacts/edit_decisions.json`): the locked settings, the cuts (or scenes), `timing_path`, `captions`, `audio`, `theme`, output size and fps; `bespoke` for atelier Remotion work.

## Review checklist

- Every cut references an asset in the manifest or is a scene type with its required fields.
- The timeline covers the full duration with no gaps or overlapping primary cuts.
- Narration and pictures agree at every moment.
- Captions come from the narration timing and do not duplicate on-screen text.
- Music is ducked under narration; settings match the proposal.

## Approval

As the pipeline sets (usually none): the cut goes to compose once `plan_check` and the critic pass.

## Tools

`plan_check`, `describe` and `estimate` for `video_compose`, `subtitle_gen`, `source_edit`, `video_trimmer`, `silence_cutter`.
Craft: `guidance/craft/editing.md`, `guidance/craft/captions.md`, `guidance/craft/sound-design.md`, `guidance/craft/hooks-and-pacing.md`.
Runtimes: `guidance/runtimes/scene-types.md`, `guidance/runtimes/remotion.md`, `guidance/runtimes/hyperframes.md`, `guidance/runtimes/composition-modes.md`. Vendor: `guidance/vendor/video-edit/SKILL.md`.
