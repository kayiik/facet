# Editing

Editing decides what the viewer sees and hears, in what order and for how long. The editor builds `edit_decisions` in the `edit` stage (method in `guidance/stages/edit.md`); this page is the cutting craft and the tools that prepare footage. Shot lengths and rhythm are in `guidance/craft/hooks-and-pacing.md`.

## Know the material first

- **`media_probe`** every source: size, frame rate, length, audio streams, rotation. Phone and screen recordings often have variable frame rates; everything is normalised to one format before it is joined.
- **`scene_detect`** lists the shots in long footage. Lower `min_scene_length_seconds` (0.3 to 0.5) for fast-cut material; `method: "adaptive"` for dissolves.
- **`frame_sample`** shows you the material before you cut it: one frame a shot (`strategy` type `scenes`), or frames at candidate in and out points (`timestamps`).
- **`silence_cutter`** with `mode: "mark"` maps speech and pauses without cutting anything; joins belong in those pauses. When the edit depends on what is said, ask the operator for a transcript: `capabilities` reports `transcription` as unavailable.

## Cutting speech

Cut:

1. **Dead air:** pauses over 1.5 s, down to about 0.5 s. `silence_cutter` with `min_silence_duration: 1.5` and `padding_seconds: 0.25` leaves 0.5 s (the padding stays on both sides). `mode: "speed_up"` plays pauses fast instead of removing them, which keeps on-screen action continuous.
2. **False starts:** keep only the final, complete take.
3. **Filler words** (um, uh, you know), at word boundaries.
4. **Repeats:** keep the best delivery of each point.
5. **Tangents:** cut to the next relevant passage.

Keep breath pauses of 0.3 to 0.8 s between sentences, deliberate emphasis pauses, and bridges ("So…", "Now…") that carry the flow.

Never change the speaker's meaning. Do not join half-sentences into a claim they did not make, drop a qualifier ("not", "sometimes", "in our tests"), or separate a question from its answer. Every edited sentence must still say what the speaker said.

## Cutting technique

- **Cut in pauses and at sentence ends,** never mid-word. Listen to every join; a click means the cut landed inside a sound.
- **Cut on action:** cut during a movement, not before or after it; the motion hides the cut. Keep the direction of movement across cuts.
- **Vary shot size** across consecutive cuts: wide, medium, close. Two shots of the same subject at the same size and angle make a jump cut.
- **Jump cuts in a talking head** suit fast short-form. Elsewhere, cover the join with a cutaway (b-roll, a screenshot, a graphic) over the continuing voice.
- **J-cut and L-cut:** the sound of the next shot starts 0.3 to 0.5 s before its picture (J), or the current sound runs 0.3 to 0.5 s over the next picture (L). In the composition the narration is its own track, so a picture can arrive just after its words begin or hold briefly over the next line. `source_edit` joins are straight cuts: picture and sound change together.
- **The cut is the default transition.** Use the others for a reason:

| Between the two shots | Transition | Length |
|---|---|---|
| same scene, continuous action, or another angle on it | cut | – |
| new topic or section | crossfade | 0.5–0.8 s |
| time passing, a change of mood (slow pieces) | crossfade | 1.0–1.5 s |
| a major break: intro to body, body to close | fade through black | 0.5–1.0 s |
| generated clips that nearly match | crossfade | 0.3–0.5 s |
| generated clips that clearly don't | fade through black, or regenerate | 0.5 s |

Short-form keeps every transition at 0.3 to 0.5 s.

## Which tool

- **`video_trimmer`:** one clip. `operation` `cut` (`start_seconds`, `end_seconds`), `speed` (`speed_factor`) or `concat`. Its `codec` defaults to stream copy, which is fast but starts the cut on the nearest keyframe; pass `codec: "libx264"` for a frame-accurate cut.
- **`source_edit`:** selects from one or more recordings into one file in one format. Give `segments` (`input`, `start`, `end`) and a `target` (size, fps, `fit`). `fit: "contain"` pads; `fit: "cover"` crops to fill, with `position` or `focal_point` keeping the subject in frame, which is how landscape footage is reframed for vertical. Inputs without sound get silence; `replacement_audio` lays one new track (a cleaned voice, a mix) under the whole edit.
- **`silence_cutter`:** dead air, as above.
- **`video_stitch`:** joins finished clips. `validate` reports mismatched size, frame rate or audio; `preview_stitch` makes a quick, rough draft; `stitch` makes the final, normalising every clip to `target_resolution`, `target_fps` and one audio format, with `transition` `cut`, `crossfade` (a dissolve) or `fade` (through black) and `transition_duration`. Each crossfade overlaps the clips and shortens the total by its length.
- **`video_compose`:** the composed timeline. Footage enters as `video` cuts that start at `source_in_seconds`.
- **`color_grade`:** one look over the assembled footage (`guidance/craft/color-grading.md`).

Order of work: select and cut, assemble, grade, compose and mix, then burn captions last so the grade never tints them.

## Clips on screen together

`video_stitch` with `operation: "spatial"`:

- **Before and after, A against B:** `side_by_side` for landscape delivery, `vertical_stack` for vertical.
- **Commentary over content, a face over a screen recording:** `picture_in_picture`, bottom right by default (`pip_position`). `pip_scale` 0.2 to 0.25 for commentary, 0.3 to 0.35 when both matter equally.
- Keep the inset off captions, faces and the content being discussed.

## Chaining generated clips

1. Write clip N+1's opening to match clip N's ending: same subject, position, light and direction of motion. With `gflow_video`, an approved frame can be its `start_frame`.
2. `frame_sample` the last frame of N and the first of N+1; compare colour, position, background and motion.
3. A small mismatch takes a 0.3 to 0.5 s crossfade; a large one a fade through black, or a new clip.
4. Drop generated sound unless the plan keeps it; one narration and one music track run over the whole sequence.
5. Grade the sequence with one profile; colour drifts between generations.

## Sound across cuts

- Music that spans several clips is mixed once over the finished edit (`audio_mix`, or `audio` in the composition), never cut along with the clips.
- When the track changes, crossfade the music over 1 to 2 s around the cut.
- Check source levels before cutting (`visual_qa` with `operation: "audio_levels"` at a few points in each source); loudness is normalised at the end, but a quiet clip next to a loud one still jumps.

## Quality checks

- Every join sits in a pause or at a sentence end: no clipped words, no clicks.
- Every edited sentence means what the speaker meant; qualifiers are kept.
- Jump cuts are deliberate or covered.
- One size, frame rate and shape throughout; nothing stretched (`video_stitch` `validate` before, `media_probe` after).
- No colour or exposure jump at joins.
- Each transition matches the relationship between its shots; nothing decorative inside a scene.
- The total length matches the plan, counting crossfade overlaps.
- Every join scrubbed in the output, with `frame_sample` frames either side.
