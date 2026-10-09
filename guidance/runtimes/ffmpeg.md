# FFmpeg

FFmpeg works on footage directly: selecting, trimming, joining, grading, mixing and burning captions. Choose `render_runtime: "ffmpeg"` when the video is cut from footage and needs no composed graphics; a composition engine adds nothing there. The tools below run on FFmpeg whatever the runtime: footage is prepared with them before it enters a composition as `video` cuts. Choosing a runtime: `guidance/runtimes/composition-modes.md`.

## Choose it when

- Cleaning up a talking head: dead air, false starts, a grade, a mix, burned captions.
- Cutting a long recording into clips, or reframing landscape footage for vertical.
- Joining finished clips (generated shots, a stock sequence) with cuts, crossfades or fades.
- Putting a new mix or new captions on an existing video, as in localization.

Not for motion graphics, charts or kinetic type. Stills with camera moves are not animation: when the brief promises motion, FFmpeg is a downgrade, and never a silent fallback.

## Tools

| Tool | Use |
|---|---|
| `media_probe` | size, frame rate, length, streams and rotation of every source before cutting, and of every output |
| `scene_detect` | the shots in long footage |
| `silence_cutter` | `mode` `mark` maps speech and pauses, `remove` cuts the pauses, `speed_up` plays them fast |
| `source_edit` | segments from one or more recordings into one file in one format: `segments`, a `target` (size, fps, `fit`: `contain` pads, `cover` crops, with `position` or `focal_point` keeping the subject), `replacement_audio` |
| `video_trimmer` | one clip: `cut`, `speed`, `concat`; stream copy by default (fast, starts on a keyframe), `codec: "libx264"` for a frame-accurate cut |
| `video_stitch` | joins finished clips: `validate`, `preview_stitch`, `stitch` with `transition` (`cut`, `crossfade`, `fade`) and `transition_duration`; `spatial` for side by side, stacked and picture in picture |
| `color_grade` | one look over the assembled footage: a `profile` and an `intensity` (`guidance/craft/color-grading.md`) |
| `audio_mix` | narration, music lowered under it (only in chosen `sections` if wanted), `sfx` cues with `at_seconds` and `gain_db`, a target duration, `loudness_target` |
| `subtitle_gen` | caption files from the `timing_path`: SRT and VTT for players, ASS for burning, a `words` file for compositions |
| `ffmpeg_caption_burn` | burns captions into a finished video at its own size: `font_size` in pixels at the video's height, `highlight_color`, `font_file` |
| `video_compose` | with `render_runtime: "ffmpeg"`, assembles media cuts without a composition; `describe` lists what that path accepts |

The cutting craft and these parameters in depth: `guidance/craft/editing.md`.

## Order of work

1. Know the material: `media_probe` every source, `scene_detect` long footage, `silence_cutter` with `mode: "mark"` for speech.
2. Select and cut: `source_edit`, `video_trimmer`, `silence_cutter`.
3. Assemble: `video_stitch`, or `video` cuts in a composition.
4. Grade once, over the assembly: `color_grade`.
5. Mix: `audio_mix`, or `audio` in the composition.
6. Burn captions last with `ffmpeg_caption_burn`, so the grade never tints them.
7. Review: `media_probe`, then `output_review`.

## Rules

- **Copy or re-encode.** Stream copy is instant and lossless but cuts only on keyframes. Filters, speed changes, scaling, captions and frame-accurate cuts re-encode.
- **One format before joining.** Mixed sizes, frame rates or audio formats are normalised first: `source_edit` with a `target`, or `video_stitch` `stitch` with `target_resolution` and `target_fps`. `video_stitch` `validate` reports mismatches before you join.
- **Fill the frame.** For a new shape, `fit: "cover"` with a focal point keeps the subject; `contain` pads with bars.
- **Loudness:** `loudness_target` −14 LUFS for social and web (the default), −16 for podcasts; broadcast has its own figure (−23 or −24 LUFS) from the broadcaster's spec. True peak at or below −1 dBTP.
- **Ducking:** music dips while the narration plays and recovers in the pauses; `audio_mix` does it. Music sits 18 to 20 dB under the voice (`guidance/craft/sound-design.md`).
- **Captions** sit in the lower fifth of the frame, clear of faces and, in vertical video, clear of the platform's buttons. `subtitle_gen` `layout` `vertical` gives 3 to 4 words a cue (`guidance/craft/captions.md`).
- **Grade gently:** an `intensity` around 0.8; 1.0 is usually overdone, more so on a phone.

## What Facet does for you

- Every tool checks its inputs before it runs and reports what it wrote.
- `video_stitch` `stitch` and `source_edit` bring every clip to one size, frame rate and audio format; `source_edit` gives silent inputs a silent track.
- `audio_mix` and `video_compose` normalise loudness after mixing or rendering.
- `ffmpeg_caption_burn` renders at the video's own size, so a font size is a pixel size.
- `output_review`: a contact sheet, narration and caption coverage, loudness.

## Quality checks

- Plays on desktop and phone without artefacts; sound and picture stay in sync after every step.
- No clicks, clipped words or silent gaps at the joins.
- One size, frame rate and shape throughout; nothing stretched.
- Captions in the lower fifth, never over a face.
- Loudness within 1 LU of the target.
- Skin looks natural after the grade, not orange.
- File size reasonable for the platform.

## Vendor knowledge

`guidance/vendor/ffmpeg/SKILL.md` and `guidance/vendor/video-edit/SKILL.md` explain the raw FFmpeg commands behind these tools. In Facet, use the tools: they check their inputs and paths and report what they did.
