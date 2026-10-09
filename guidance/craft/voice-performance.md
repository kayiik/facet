# Voice performance

Generated narration should sound directed, not read. Carry a concrete performance plan from the `script` to the voice tool, prove it on a sample, then voice the whole script the same way. "Read naturally" is not a direction.

## The performance plan

The writer puts it in the `script`, at the top:

- **Intent:** who is speaking, to whom, and how ("a warm, decisive guide explaining to a smart beginner").
- **Pacing profile:** conversational (150 words a minute), technical (130), contemplative (120) or energetic (180 to 200). See `guidance/craft/hooks-and-pacing.md`.
- **Energy curve:** "measured hook, warmer middle, slower and deliberate close"; for a product, "brisk and confident, slowing on the result"; for cinematic, "low and close, lifting at the reveal".
- **Pause policy:** short pauses after setup lines, longer ones before reversals and after surprising claims, the longest after the key insight.
- **Sample section:** the most performance-sensitive section, not automatically the first.

Each narrated section carries at least two concrete delivery cues from: pace, energy, emphasis words, pause after, a delivery note ("set up the contrast, then slow on the last phrase"), or text rewritten for the voice. Avoid "natural", "engaging" or "expressive" unless paired with one of those.

## Write for the ear

- Short sentences, light contractions, clear punctuation. Commas and full stops are the only direction an offline voice gets.
- One delivery idea a line. A sentence with three emotional turns becomes three lines.
- Write numbers, units and abbreviations the way they should be said when the voice gets them wrong ("twenty twenty-six", "three point five", "S-Q-L" or "sequel"), and keep the caption files in the real spelling.
- Give pronunciation for names, acronyms and technical terms in the script ("Qdrant: kuh-DRANT", "cosine: CO-sign") and test them in the sample. When a voice still gets one wrong, respell it in that line only.
- No directions a voice cannot perform: "smile", "gesture to the screen", "look at camera".

## Lines and timing

Voice the narration as `lines`, never as one block of text:

```json
{"lines": [
  {"id": "l1", "text": "Your database reads every single row.", "pause_after_seconds": 0.4},
  {"id": "l2", "text": "Every. Single. One.", "pause_after_seconds": 0.7},
  {"id": "l3", "text": "What if it didn't have to?", "pause_after_seconds": 0.5}
]}
```

- One sentence or phrase a line, ids in order (`l1`, `l2`…), matching the script's sections.
- Keep a line short enough to caption: at most two caption lines of 42 characters, about 15 words or 6 seconds of speech.
- Pauses live in `pause_after_seconds`; a pause before a line is the pause after the line ahead of it. Prefer splitting lines to break tags inside the text; tags work only with some voices.
- The tool voices each line, measures it, joins them with the pauses into one narration file, and writes the `narration_timing` record (`timing_path`, by default next to the output as `<name>.timing.json`): each line's start and end, and each word's start and end. `edge_tts` gives real word timings; the other voices spread words evenly over each line.
- That record drives everything downstream: `subtitle_gen` builds captions from it, `video_compose` cuts can name `lines` instead of seconds, its `captions` read it, and `output_review` checks coverage against it. Never retime by hand what the record already knows.

## Choosing a voice

| Tool | Runs | Controls | Best for |
|---|---|---|---|
| `edge_tts` | network, no key, free | `voice`, `rate` ("-10%" to "+10%"), `volume` | the default; real word timings make the best captions |
| `piper_tts` | local, offline, free | `model`, `speaker_id`, `length_scale` (1.1 slower, 0.9 faster), `sentence_silence` | private or offline work |
| `openai_tts` | paid, uses the operator's credits | `voice`, `model`, `instructions`, `speed` | direction in plain words: role, arc, emphasis |
| `elevenlabs_tts` | paid, uses the operator's credits | `voice_id`, `model_id`, `stability`, `similarity_boost` | the most expressive and natural reads |

- Match the voice to the audience and the style: warm and mid-pitched for explainers, crisp for product, low and unhurried for cinematic; an accent the audience hears as their own.
- One voice for the whole video, unless there are characters.
- `openai_tts`: put the role, emotional arc, pace and emphasis words in `instructions` with a model that accepts them (`gpt-4o-mini-tts`); keep the text clean and punctuated; keep `speed` between 0.9 and 1.1.
- `elevenlabs_tts`: lower `stability` (0.3 to 0.5) gives a more varied, expressive read; raise it for steady corporate narration. Keep `similarity_boost` high (about 0.75) to hold the voice. More in `guidance/vendor/elevenlabs/SKILL.md`.
- `edge_tts` and `piper_tts` have no emotion control: rely on punctuation, short lines, pauses and the choice of voice.
- Rate and speed apply to the whole call. Vary pace inside the narration with sentence length and pauses, not by changing settings between lines.

General voice knowledge is in `guidance/vendor/text-to-speech/SKILL.md`.

## The sample gate

Before voicing the whole script:

1. Voice the sample section as `lines` with the planned settings.
2. Check it: words a minute from the `narration_timing` (spoken words divided by speaking time) within 10% of the plan; pauses where the script put them; emphasis words landing; names said correctly.
3. Give the operator the sample to hear, with the voice and settings used.
4. If it is flat or wrong, change the plan, the voice or the settings and sample again. Paid voices are always sampled before the batch.
5. Record the approved sample, voice and settings in the `asset_manifest`.

After approval, never change the voice, provider, model or speed without a new sample.

## After the full narration

- Compare the timing's total length with the planned duration. More than a second over: cut words and voice again, or extend the closing scene.
- Check each line's length against the scenes it must cover; a line much longer than its scene moves the cut or gets shorter.
- When one line is wrong (a misread name, a flat key line), fix that line's text or pause and voice the narration again with the same settings, so every line matches.

## Quality checks

- A narration-led script has a performance plan, and every section at least two concrete cues.
- The narration was voiced from the `lines`, not from raw script text.
- Pace within 10% of the plan; the key insight followed by its pause; no rushed hook.
- Names, terms and numbers said correctly; captions spelled correctly.
- Voice settings unchanged since the approved sample.
- No clipped line starts or ends, no clicks between lines, no level jumps between lines.
