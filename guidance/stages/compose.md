# Stage: compose

**Run by:** the editor.
**Record:** `render_report`.

The video becomes a playable file: rendered with the engine and mode that were locked, with its mix, captions and loudness, in the frame size the platform needs.

## Inputs

`edit_decisions`, `asset_manifest`, `narration_timing`.

## Method

1. **Route by the locked engine.** Read `render_runtime` and `composition_mode` from `edit_decisions`; they were agreed at the proposal and do not change silently.
   - **remotion:** `video_compose` with the edit decisions. Templated work renders the stock compositions (`Explainer`, `CinematicRenderer`, `TalkingHead` and the others); atelier work renders the project's own entry through `bespoke`.
   - **hyperframes:** `video_compose` with `render_runtime` `hyperframes` and the workspace (or `hyperframes_compose` directly). Lint and validate must pass before the render; spot-check frames at the beats before a full render.
   - **ffmpeg:** cutting and joining footage: `video_compose` on its FFmpeg path, `video_stitch`, `source_edit`.
2. **If the engine is unavailable or fails,** stop. Tell the operator what was attempted, what failed, whether it is setup, provider access, a tool fault or the design, the options, and your recommendation. Never swap engines, or turn motion into stills, without agreement.
3. **Before rendering:** every source exists; the narration is not longer than the video; the music covers the duration or loops; `estimate` reports the expected render time. When it says `prefer_shell`, run `facet tools run video_compose --input <file>` in the shell (in the background where the host allows) instead of through MCP.
4. **Sound.** `video_compose` lowers music under the narration with every engine and normalises loudness after rendering. A footage-only render without a composition gets its mix from `audio_mix`: narration, music lowered under it (optionally only in chosen `sections`), effect cues, target duration, `loudness_target` (default −14 LUFS).
5. **Picture finishing.** One consistent grade across mixed sources with `color_grade` when the treatment calls for it. Burned captions go on last with `ffmpeg_caption_burn` (size in pixels at the video's height, `highlight_color`, `font_file`).
6. **Several deliverables** (clips, frame sizes, languages): one render each, named by what it is: `renders/<name>-<variant>-<ratio>.mp4`. Render the most publishable first. If one fails, report it and finish the rest.
7. **Write the render report,** then move straight to the review stage. A zero exit code is not acceptance.

## The record

`render_report` (`artifacts/render_report.json`): each output with path, format, codecs, resolution, fps, duration, file size and target platform; render time; warnings; verification notes.

## Review checklist

- The output exists and plays; duration within 5% of the target.
- The engine and mode used are the ones locked at the proposal.
- HyperFrames work passed lint and validate before the render.
- Narration audible throughout; music balanced; loudness normalised.

## Approval

None. The review stage follows immediately.

## Tools

`video_compose`, `hyperframes_compose`, `audio_mix`, `color_grade`, `video_stitch`, `video_trimmer`, `source_edit`, `subtitle_gen`, `ffmpeg_caption_burn`, `media_probe`; planning: `describe`, `estimate`.
Runtimes: `guidance/runtimes/remotion.md`, `guidance/runtimes/hyperframes.md`, `guidance/runtimes/ffmpeg.md`, `guidance/runtimes/composition-modes.md`, `guidance/runtimes/scene-types.md`.
Vendor: `guidance/vendor/remotion-best-practices/SKILL.md`, `guidance/vendor/hyperframes/SKILL.md`, `guidance/vendor/hyperframes-cli/SKILL.md`, `guidance/vendor/ffmpeg/SKILL.md`.
Craft: `guidance/craft/color-grading.md`, `guidance/craft/sound-design.md`, `guidance/craft/captions.md`.
