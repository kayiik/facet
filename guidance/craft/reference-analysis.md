# Reference analysis

A reference is a video the operator gives as inspiration ("make something like this"), not footage to edit. Footage to cut goes to `source_media_review` instead. The job is to learn what the reference does and why it works, then make something that keeps the reasons and changes the rest. Never a carbon copy.

The `facet-reference-analyst` agent does the analysis and writes `video_analysis_brief`; the producer turns it into questions and proposals.

## Inspect

1. **`media_probe`** the file: duration, frame size, fps, audio streams. If you only have a link that cannot be fetched, ask for the file or a description.
2. **`scene_detect`** for the shots. For fast-cut references lower `min_scene_length_seconds` (0.3 to 0.5) so quick cuts are not merged; for slow dissolves try `method: "adaptive"`.
3. **`frame_sample`** one frame per shot (`strategy: {"type": "scenes", ...}`), or 12 to 20 evenly spaced frames (`"uniform"`) when detection fails. Sample the first 3 seconds densely (`"timestamps"`: 0, 0.5, 1, 2, 3) to see the hook. Look at every frame yourself.
4. **Speech:** transcribe only when the `transcription` capability is available or the operator supplies captions. Otherwise note "no transcript" and read any burned-in text from the frames.

## Measure structure and pacing

- Shots, average shot length (duration ÷ shots), shortest, longest, cuts a minute ((shots − 1) ÷ duration × 60).
- Pacing style: **rapid** (average under 2 s), **dynamic** (2 to 4 s), **steady** (4 to 8 s), **contemplative** (over 8 s), **variable** (passages differ by more than double). Note where the rhythm changes and what happens there.
- Narration pace in words a minute when there is a transcript; whether the voice starts at once; where the music drops out.

## Classify motion: never guess

For each shot, compare its start, middle and end frames (`frame_sample` with `timestamps`):

- **Motion clip:** subjects move on their own, parallax between layers, light or perspective changes. Implies stock video, supplied footage or generated video.
- **Animated still:** the whole frame scales or slides uniformly (pan and zoom over one image). Implies images with camera moves in `Explainer` `image` cuts.
- **Static image or graphic:** nothing changes, or only overlays animate. Implies composition scene types or images.

Count each kind. This decides the production method: proposing generated video for a reference built from animated stills, or the reverse, wastes the whole production.

## Five aspects, shot by shot

Walk every shot, or group of similar shots, through all five aspects in order. Mark an aspect "N/A" when it does not apply ("Subject: N/A, pure landscape"). Silent omission is the most common failure.

1. **Subject:** type, count, attributes (age, role, costume, distinguishing features); how to tell several apart; subject changes across shots: revealing, disappearing, switching, alternating.
2. **Subject motion:** actions in order; interactions (parallel, sequential, reactive); travel versus gesture versus expression.
3. **Scene:** overlays first and separately (text, lower thirds, graphics, watermarks); point of view (drone, aerial, over-the-shoulder, macro, top-down, dashcam, handheld, locked-off); setting; time of day; dynamics (weather, particles, crowds).
4. **Spatial framing:** shot size (extreme close-up, close-up, medium, wide, extreme wide); position in frame; depth (foreground, middle, background); camera height against the subject; how each changes during the shot.
5. **Camera:** playback speed (real time, slow motion, time-lapse), lens (normal, wide, anamorphic, fish-eye, tilt-shift), height (ground, eye, overhead), angle (high, low, Dutch), focus (deep, shallow, rack), steadiness (locked, handheld, gimbal), movement (push, pull, pan, tilt, dolly, truck, crane, orbit).

Keep the labels in the brief. The `proposal`, `script` and `scene_plan` stages lift these fields directly; prose loses them.

## Content and style

- **Content:** a two-sentence summary, topics, key claims, audience, tone, the hook technique (what happens in the first 3 seconds), the call to action.
- **Look:** dominant and accent colours across the frames, typography (faces, sizes, placement, how text enters), transitions seen between consecutive frames, production quality.
- **Sound:** music style and energy, narration (present or not, speakers, pace, delivery), sound effects, subtitles and their style.
- **Closest Facet style** (`clean-professional`, `flat-motion-graphics`, `minimalist-diagram`, `premium-minimalist`, `anime-ghibli`) and how the reference differs from it.

## What makes it work

Name two or three specific causes, not adjectives: "the hook shows the result before the question", "every third shot is a close-up on hands", "music cuts out a beat before each reveal". These are what the operator's version keeps.

## Map it to Facet

- **Fit:** the pipeline that matches (`pipelines_list`, `pipeline_describe`), the renderer family, the elements worth keeping, the ones needing custom (atelier) work, complexity (simple, moderate, complex, beyond current capability), and whether real motion is required.
- **Gaps:** check `capabilities` against what the reference needs: real motion needs `video_stock`, `video_generation` or supplied footage; animated stills need `image_stock` or `image_generation`; a presenter needs `avatar` (unavailable for now) or real footage. Say plainly what is missing and offer the alternative: stock footage plus composition animation instead of generated clips, motion graphics instead of a presenter.

## Turn it into proposals

The producer, before proposing:

1. **Ask what the brief cannot tell you,** one question at a time, the most important first, skipping anything already answered: narration or visuals with music only; if narration, one narrator, character dialogue, or a narrator with character lines (settle this before proposals); the length (the reference's, or theirs); the subject; what they love or dislike in the reference.
2. **Research lightly:** three to five similar videos (what is overdone, what is fresh), how the distinctive technique is achieved, and three to five facts if the subject is factual.
3. **Propose two or three variants,** each stating what it keeps (pacing, structure, tone) and its twist. Recommend one, with a reason.

| Differentiation | Example |
|---|---|
| Same structure, new subject | "How black holes work" becomes "How neutron stars work", same pacing |
| Same subject, new angle | "Kubernetes explained" becomes "Kubernetes from a security engineer's view" |
| Same tone, new treatment | stock footage and voice-over becomes animated motion graphics and voice-over |
| Same content, new format | a 10-minute video becomes a 60-second vertical cut with faster pacing |
| Counter-take | "Why AI will replace jobs" becomes "Why AI won't replace your job" |

4. **Sample first:** before full production, make 10 to 15 seconds (the hook and one middle scene) with the real voice, visuals, music and caption style, and let the operator react. Then run the pipeline stage by stage, with the brief as grounding.

## Several references

Analyse each one separately, compare them ("A does the hook well, B the pacing"), and note which element of each variant comes from which. The primary reference's brief travels with the project; the others are noted in the `research_brief`.

## When a step fails

Say what failed and what it costs the analysis; never skip silently. Detection fails: sample uniformly. No transcript: work from frames. The file cannot be read: ask the operator to describe the video and continue with a normal `intake`.

## Quality checks

- Shot count, average shot length and cuts a minute are measured, not estimated.
- Motion is classified from frames compared within each shot.
- All five aspects are present for every shot or group, or marked N/A; overlays are listed apart from the setting.
- "What makes it work" names causes.
- Gaps against `capabilities` are stated with alternatives.
- Every proposal differs from the reference in at least one named way.
