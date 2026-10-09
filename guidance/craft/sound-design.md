# Sound design

Sound carries half the video and most of the feeling. Viewers forgive a soft image long before they forgive muddy speech, music fighting the voice, or a level jump between clips. The sound designer plans it from the `script` and `scene_plan`, mixes it in `edit` and `compose`, and the critic checks it in `review`.

## Levels

| Element | Level | Notes |
|---|---|---|
| Narration, dialogue | peaks -12 to -6 dB; about -16 to -14 LUFS on its own | the reference everything else sits under |
| Music under speech | 18–20 dB below the voice | if you can follow the melody over the words, it is too loud |
| Music with no speech | up to 6–12 dB higher than under speech | intros, outros, breathers, montage |
| Sound effects | -18 to -12 dB peaks; impacts up to -6 dB | at least 6 dB below the voice |
| Ambience, room tone | -30 to -24 dB | felt, not heard |
| Final mix | -14 LUFS integrated for social and web; true peak at or below -1 dBTP | -16 LUFS for podcasts |

When in doubt, set the music lower than sounds right, then lower it another 4 dB. Many viewers listen on phone speakers, where music swamps speech first.

## Music

**Choose for the content:**

| Content | Tempo |
|---|---|
| Calm explainer, tutorial, testimonial | 60–80 BPM |
| Standard explainer, education | 90–110 BPM |
| Upbeat explainer, promo | 110–130 BPM |
| Product demo, high energy | 120–140 BPM |
| Cinematic | 60–90 BPM, dynamic, not loop-based |
| Action, fast montage | 140 BPM and up |

- Instrumental only under a voice; lyrics compete with words.
- Under narration, prefer tracks with even dynamics and nothing busy in the voice's range (lead melodies, vocal samples). Lo-fi, ambient, light acoustic and soft electronic beds stay out of the way.
- Cinematic and music-led work wants the opposite: builds, drops and key changes placed on the story's turns.
- Short-form: music starts on frame one. Long-form: fade in over 1 to 2 s, fade out over 2 to 3 s; end on a phrase rather than cutting mid-note.
- Silence is an effect: drop the music for 1.5 to 3 s before a reveal or under the key insight, and 3 to 5 s at a cinematic reveal, then bring it back.

**Find it:** `music_library` lists the tracks in the local music folder (`library_dir`, or the configured default) with their durations. Pick a track at least as long as the video, or plan where it loops or ends. When there is no library, or nothing fits, ask the operator for a track, or make the video without music; never assume one exists.

## Sound effects

| Effect | Use | Length | Peak |
|---|---|---|---|
| Whoosh, swish | transitions, slides | 400–500 ms | -18 to -12 dB |
| Soft whoosh | an element sliding in or out | 200–400 ms | -20 to -15 dB |
| Pop, pluck | text or bullets appearing | under 200 ms | -15 to -12 dB |
| Click, tap | interface clicks, cursor presses | under 100 ms | -20 to -15 dB |
| Riser | building to a reveal | 1–3 s | -18 to -12 dB |
| Impact, hit | a key reveal or number | under 300 ms | -12 to -6 dB |

**Timing:** sound reaches the brain before the picture, so effects lead slightly. Start a whoosh 10 to 20 ms before the visual change, with its loudest point on the moment of greatest change (mid-transition for a slide, the cut frame for a hard cut). End a riser exactly on the reveal. Nudge in one-frame steps (33 ms at 30 fps).

- Use effects where the picture changes meaningfully, not on every element. One clean whoosh is better than five layered.
- Stacked effects sit in different frequency ranges (a low impact with a high shimmer, not two mid whooshes).
- Effects follow the style: soft clicks and plucks for clean, premium styles; punchier hits for promos.

**Source:** the `sfx` capability has no provider yet. Use effect files the operator supplies, or a local folder of effects; `music_library` pointed at that folder lists them with durations. If there are none, ask, or go without; music and pauses can carry transitions.

## Layers for cinematic work

Narration or dialogue; score; ambience or room tone under everything, so pauses never fall into digital silence; effects and foley at the moments that matter. Intimate scenes drop the music under speech and keep room tone.

## Mixing in Facet

- **`audio_mix`:** narration plus music lowered under it, with optional `sections` that say where music plays (for example the intro, a breather and the outro only). Sound effects go in as cues, `sfx: [{"src": "...", "at_seconds": 12.38, "gain_db": -6}]`; set each `gain_db` so the effect peaks in its range above. The mix runs to the target duration and normalises to `loudness_target` (default -14 LUFS).
- **`video_compose`:** `audio` with `narration` and `music` with `duck_under_narration: true`; music dips while narration plays and recovers between lines. It normalises loudness after rendering.
- **Placing cues:** take times from `narration_timing` and the cut list, not guesses. A whoosh on a transition at 12.40 s goes at about 12.38 s; an impact on a stat lands on the line's word ("forty percent") from the word timings.
- **Checking:** `output_review` with `coverage` reports loudness, narration coverage and gaps; `visual_qa` with `operation: "audio_levels"` reports the audio levels; `audio_probe` gives the file's format and duration.

## Quality checks

- Integrated loudness at the target within 1 LU (-14 LUFS social and web, -16 podcasts); true peak at or below -1 dBTP.
- Every word of narration is clear over the music; music sits 18 to 20 dB under speech.
- No lyrics under narration.
- Effects land on their visual events, whooshes slightly ahead; none are random or constant.
- Music starts and ends cleanly; silences are deliberate, never dead air between clips.
- No level jumps between scenes or sources; no clicks or pops at cuts.
- Listen once on a phone speaker, or ask the operator to, before `publish`.
