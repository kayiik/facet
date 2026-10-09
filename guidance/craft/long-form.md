# Long-form

A long video (8 minutes and more; most topics fit 8 to 15) is a series of promises kept one after another. Viewers tend to leave at three points: in the first 30 seconds if the hook fails, around minutes 2 to 3 when first curiosity is spent, and past the middle when the video stops renewing its reasons. Structure, re-hooks and steady change on screen carry them through. Story shapes are in `guidance/craft/storytelling.md`, hook and pace numbers in `guidance/craft/hooks-and-pacing.md`.

## Chapters

- 2 to 4 minutes a chapter: a simple concept or a demonstration 2 to 3, a complex concept 3 to 4, a story 3 to 5. At most 5 to 6 chapters in 10 to 15 minutes; more feels fragmented.
- Each chapter has one job, opens with a re-hook and a visible change (a `section_title`, a new dominant scene type, a new place), and ends on a "but" or a "therefore" that pulls into the next.
- Pick the structure from `pipeline_describe` and give each chapter the share of time its beat implies; `script_check` reports the shares.

A 12-minute shape:

| Part | At 12 min | Job |
|---|---|---|
| Intro | 0:00–0:30 | the strongest shot in the first 3 s; the hook landed by 5 s; stakes by 15 s; a preview and one open question by 30 s |
| Chapter 1 | 0:30–3:00 | the foundation; a first real payoff before 2:00; an interrupt at 1:45–2:00 |
| Re-hook | 3:00–3:15 | what comes next and why it matters |
| Chapter 2 | 3:15–6:00 | the complication |
| Breather | 6:00–6:15 | a held frame, a visual joke, "let that sink in" |
| Chapter 3 | 6:15–9:00 | the key insight, then 1 to 3 s of silence |
| Proof | 9:00–10:30 | a demonstration or a case; close the open question |
| Conclusion | 10:30–11:30 | what it means for the viewer; call back the hook |
| Outro | last 20–30 s | one call to action and an end card; nothing essential |

A branded intro, if any, comes after the hook and lasts at most 5 s.

## Keep renewing the reason to watch

- **Open questions early, answered late.** Raise one in the first minute and pay it off in the proof.
- **Re-hooks** at about 2 minutes and every 3 to 4 minutes after: "but that's not the interesting part", "here's where it gets strange". Each one promises something specific that comes next.
- **Major pattern interrupts every 60 to 90 s:** a change of visual treatment, a music change at a chapter boundary, a burst of 5 to 10 quick cuts over 10 to 15 s, then calm.
- **Minor interrupts every 20 to 30 s:** a cutaway, on-screen text for a key number or term, a direct question to the viewer, a sound accent.
- **Something must happen:** a small change every 3 to 5 s, a substantive one (new scene, new layout) every 20 to 30 s, never 15 s with nothing changing.
- In presenter-led videos, b-roll and graphics cover 35 to 50% of the running time.
- Callbacks reward attention: bring back an earlier image, number or phrase when it pays off.

## Narration

- About 150 words a minute, 130 for technical material: about 1,500 to 1,800 words for 12 minutes at 150.
- Vary pace with sentence length and pauses rather than speed: a brisk hook, steady explanation, the key insight slower, followed by 1 to 3 s of silence (`pause_after_seconds`).
- A voice that sounds fine for 30 s can wear over 12 minutes. Sample a full chapter before voicing the rest, with the most natural voice configured (`guidance/craft/voice-performance.md`).

## Producing it in Facet

1. **Sample first:** one chapter end to end (voice, pictures, captions, music) approved before the rest.
2. **One narration or one per chapter.** A single set of `lines` with one timing record keeps cuts, captions and coverage in step for the whole video. For very long pieces, work chapter by chapter: each chapter its own lines, timing record and render; join them with `video_stitch` (`validate` first; `cut` between chapters, `fade` at a major break), then lay the music and the loudness pass over the joined video with `audio_mix`.
3. **Check the plan as a whole.** `plan_check` flags three same-type scenes in a row and slideshow risk, which long videos invite. Give each chapter a different dominant treatment.
4. **Render from the shell when told.** Run `estimate` on `video_compose`; when it reports `prefer_shell`, render with `facet tools run` in the background.
5. **Review the whole.** `output_review` with `coverage` for narration and captions across the full length, plus the `contact_sheet`.

## Sound over a long run

- Music under speech sits 18 to 20 dB below the voice (`guidance/craft/sound-design.md`).
- One looped track wears thin over 12 minutes. Change track at chapter boundaries when rendering by chapter, or play music only in `sections` (intro, breathers, bursts, outro) and let the explanations run on the voice alone.
- Keep tempos within about 10 BPM across the video unless a chapter's change of mood calls for more.
- Chapters stay within about 2 dB of each other (`visual_qa` with `operation: "audio_levels"` at points in each chapter); the whole mix is −14 LUFS, true peak at or below −1 dBTP.

## Delivery

- Keep the last 20 s free of anything essential; platforms cover it with end-screen elements. A held final scene or an `EndTag` card works.
- Write chapter timestamps for the description from the `narration_timing` line where each chapter starts, after the final edit. YouTube needs the first at 0:00, at least three, each at least 10 s long.
- Deliver the subtitles as an SRT or VTT file beside the video (`guidance/craft/captions.md`).

## Quality checks

- Hook and stakes done by 0:30; a real payoff before 2:00; a re-hook near 2:00 and every 3 to 4 minutes after.
- Chapters of 2 to 4 minutes, at most 5 to 6 in 10 to 15 minutes, each opening with a visible change.
- A major interrupt every 60 to 90 s, a minor one every 20 to 30 s; never 15 s without a change.
- `plan_check` passes: no three same-type scenes in a row, slideshow risk low.
- Every open question is answered; the close calls back the hook with one specific action.
- No audible music loop; chapter levels match; −14 LUFS overall.
- Narration and caption coverage complete across the full length (`output_review`).
- The last 20 s carry nothing essential; chapter timestamps match the final cut.
