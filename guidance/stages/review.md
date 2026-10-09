# Stage: review

**Run by:** the critic: the `facet-critic` agent where the host supports separate agents (it did not write what it judges, so it reviews without the writer's bias); otherwise you, from this guide, judging the work as if someone else made it.
**Records:** `final_review` for the rendered video; a `review` record for each critique.

## When the critic reviews

Four times, each against that stage's review focus and success list (`pipeline_describe` with the stage), the style, the taste profile and the stance's quality bar:

1. **the concepts** (proposal): genuinely different, grounded in the research, feasible with the configured providers;
2. **the script:** hook, turn, beats that earn their place, length, voice, facts;
3. **the cut** (edit): timeline against script and scene plan, pacing, captions, sound, delivery promise;
4. **the final video** (this stage).

Every other stage gets a self-review against its checklist before it is presented.

## How to critique

- **Accurate.** Every finding points at something: a field, a line, a timestamp, a frame. If you cannot point to it, you are guessing.
- **Complete.** When you find one problem, look for the rest of its kind before you return.
- **Constructive.** Every critical finding carries a concrete fix ("Section 3 is 180 words for 10 seconds; cut to 25 words", not "too long"). A concern you cannot fix precisely is an investigation note, not a critical finding.
- **Severity.** Critical: broken, false, missing, or against the approved plan; must be fixed. Suggestion: clearly better if fixed. Nitpick: polish. Do not inflate.
- **Decide.** No critical findings: pass, with suggestions noted. Critical findings: revise, then review again. At most two rounds; after the second, pass with the open issues recorded as warnings. Never block indefinitely.

A `review` record (`artifacts/review-<stage>-<round>.json`) holds the stage, the round, and the findings, each with an id, a severity, a description that says where, what is wrong and the fix, and its disposition.

## Final review method

1. **Measure with explicit expectations.** Call `output_review` with the explicit expected profile (width, height, fps), the duration with its tolerance, the video codec, the pixel format, and audio presence (required, codec, sample rate, channels), plus frame samples and an evidence directory under `review/`. Checks you omit are reported `assumed`, not verified. Add `contact_sheet`, and `coverage` with the `timing_path` and the `captions_path`, to measure narration and caption coverage, gaps and loudness (`describe` gives their exact shape). For example:

   ```json
   {"input": "renders/final.mp4", "profile": {"width": 1920, "height": 1080, "fps": 30},
    "checks": {"duration": {"expected": 60, "tolerance": 1.5}, "video_codec": "h264", "pixel_format": "yuv420p",
               "audio": {"required": true, "codec": "aac", "sample_rate": 48000, "channels": 2}},
    "samples": {"type": "uniform", "count": 12}, "evidence_dir": "review/evidence"}
   ```

2. **Look at the pictures yourself:** the contact sheet and a frame from the middle of every scene (`frame_sample`). Blank or frozen frames, wrong backgrounds, stretched or missing media, broken overlays, unreadable or clipped text, captions in a platform's dead zone, a weak opening frame, a wrong name, number or call to action. `visual_qa` adds frame-level checks when you need them.
3. **Listen.** Narration present and complete to the last word; music audible and lowered under the voice; no clipping, no unexpected silence; loudness on target.
4. **Check the promise.** The delivery promise is visible: a motion-led video moves, a source-led video is led by its source, a data explainer shows its data. The engine and mode used are the ones locked. Nothing was silently downgraded.
5. **Check the content.** Facts on screen match the research; supplied speech keeps its meaning and context; product behaviour shown is real, or labelled as illustration.
6. **Atelier work: distinctness.** Could this be any other product's video? Does it reuse a look from an earlier piece? Does each scene have its own primary subject? Does the signature device appear in one or two beats rather than everywhere? A "yes, no, no, no" pattern fails.
7. **Judge it as the audience would,** against the stance's quality bar and its list of things to avoid.
8. **Send fixes to the stage that can make them:** audio to compose, pictures to assets or the scene plan, length to the script, sync to the edit. Re-render and review again, at most two rounds.

## The record

`final_review` (`artifacts/final_review.json`): the output path; status (pass, revise or fail); checks for the technical probe, the visual spot-check, the audio spot-check, promise preservation (delivery promise honoured, engine used, any downgrade) and captions (expected, present, coverage, drift); the issues found; and the recommended action (present to the operator, re-render, revise the edit, revise assets, block).

## Approval

None at this stage. The producer presents the video, the review summary and the known limits at the delivery approval in `publish`.

## Tools

`output_review`, `frame_sample`, `visual_qa`, `media_probe`, `audio_probe`; `script_check` and `plan_check` for the script and cut reviews; planning: `pipeline_describe`, `describe`, `guidance`.
Craft: `guidance/craft/storytelling.md`, `guidance/craft/hooks-and-pacing.md`, `guidance/craft/taste-direction.md`, `guidance/craft/captions.md`, `guidance/craft/sound-design.md`.
