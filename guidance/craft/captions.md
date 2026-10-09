# Captions

Much social video is watched with the sound off, and then the captions carry the message. They must match the voice to the word, read in one glance and never cover what matters. In Facet every caption comes from the narration's own timing, so captions, cuts and coverage checks agree. Type sizes, safe areas and contrast are in `guidance/craft/typography.md`.

## Where the timing comes from

Give the voice tool (`edge_tts`, `piper_tts`, `openai_tts`, `elevenlabs_tts`) the script as `lines` with a `timing_path`. It writes a `narration_timing` record: each line's start and end, and each word's. `edge_tts` gives real word timings; the other voices spread the words over each line, so their word highlights can drift inside long lines. When word-exact captions matter, prefer Edge or keep lines short (`guidance/craft/voice-performance.md`).

Never retime by hand what the record already knows. If the narration changes, synthesise it again and rebuild the captions from the new record.

## Choose the kind

| Kind | What it shows | Use for |
|---|---|---|
| Subtitles | every spoken word, in cues of a sentence or part of one | long-form, accessibility, any video with dense narration |
| Word-highlight captions | pages of 3 to 4 words, the spoken word lit in the accent colour | short-form and social video, energetic pieces |
| Emphasis text | a key word or number as on-screen text | not captions: a scene's own text (`callout`, `stat_card`) |

Decide once per video. Captions and on-screen text never show the same words at the same moment, and never overlap: place scene text clear of the caption band, or choose the captions' `position` so the two never meet.

## Two ways to put them on screen

**Drawn by the composition.** `video_compose` reads `captions` in `edit_decisions`:

```json
"captions": {"timing_path": "assets/audio/narration.timing.json", "words_per_page": 4, "highlight_color": "#FFD400", "position": "bottom"}
```

- `words_per_page`: about 4 in landscape, 3 in vertical.
- `highlight_color`: one accent from the style, the same all video.
- `position`: `bottom` by default; `top` when lower thirds or other text sit low in the frame; `center` only for short-form where the captions are the main text.

**Files and burning.** `subtitle_gen` builds cues from the `timing_path` by rule: `layout` `horizontal` gives 6 to 8 words a cue, `vertical` 3 to 4, at most 42 characters a line and 2 lines. It writes SRT and VTT for players and platforms, ASS for burning, and a `words` JSON for compositions that draw their own captions (atelier work). `ffmpeg_caption_burn` burns the cues into a finished video at the video's own size: `font_size` in pixels at the video's height (about 4%: 43 px at 1080, 77 px at 1920), `highlight_color`, `words_per_page`, and `font_file` for the style's face. `describe` lists its input fields. Use this path for footage edits, FFmpeg renders and any video already rendered.

## Burn or deliver as a file

- **Short-form vertical:** burn them in or draw them in the composition. Viewers scroll muted and platform captions vary.
- **Long-form:** deliver SRT or VTT with the video so viewers can switch them off and platforms can translate them. Burn them only when the operator asks or the look depends on them.
- Burn last, after grading, so the grade never tints the text.

## The numbers

| Rule | Value |
|---|---|
| Line length | at most 42 characters; vertical lines near 20 to 30 |
| Lines a cue | at most 2 |
| Words a subtitle cue | 6 to 8 landscape, 3 to 4 vertical |
| Time on screen | at least 1 s, at most 6 to 7 s |
| Reading speed | at most 21 characters a second; about 15 reads comfortably |
| Start | on the first word's onset, never before the voice |
| End | about 0.2 s after the last word, never into the next cue; about 2 frames between cues |
| Size | 42 px or more at 1080 landscape; 56 to 80 px bold in vertical |
| Place | at least 60 px above the bottom in landscape; above the bottom 320 px in vertical; within 90% of the width |
| Contrast | white with a 2 to 4 px dark outline, or a black box at 70 to 80% |

## Line breaks and spelling

- Break at phrase boundaries: after punctuation, before "and", "but", "because" and prepositions. Never split a name, a number from its unit, or an article from its noun.
- Prefer a short top line over a long one, and one line over two when it fits.
- Names, terms and numbers are spelled exactly as in the `brief` or `research_brief`.
- A word respelled for the voice ("kuh-DRANT") is captioned as respelled. `subtitle_gen` `corrections` maps it back in caption files (`{"kuh-DRANT": "Qdrant"}`); captions drawn by the composition come straight from the timing record, so check them, and rephrase rather than respell where captions show.
- Translated captions are built from the translated narration's own timing record, never from the source timing.

## Footage without narration timing

Supplied speech has no timing record, and `capabilities` reports `transcription` as unavailable. Ask the operator for subtitles or a timed transcript; `subtitle_gen` builds cues from timed text segments, and `ffmpeg_caption_burn` burns them. If a narrator re-voices the piece, caption the new narration from its own record.

## Quality checks

- `output_review` with `coverage` (`timing_path`, `captions_path`) shows captions covering all the narration, with no gaps.
- No cue over 2 lines or 42 characters a line; vertical pages of 3 to 4 words.
- Cues start with the voice and never linger; spot-check with `frame_sample` at line starts taken from the timing record.
- Every name, term and number is spelled as sourced; no respellings left on screen.
- Captions sit inside the safe area, clear of platform buttons, faces, lower thirds and on-screen text; check frames at phone size.
- Contrast holds on the graded frames, over the brightest and busiest backgrounds.
- The highlight colour is the style's accent and the same throughout.
