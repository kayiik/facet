# Hooks and pacing

The hook is the promise; pacing is how fast the video pays it out. Both are set in the `script`, planned in the `scene_plan` and enforced in the cut.

## The hook

| Format | Hook lands by | Hook and stakes done by |
|---|---|---|
| Vertical short-form (up to 60 s) | 1–2 s; hook text on screen within 0.5 s | 5 s |
| Horizontal, under 3 minutes | 2–5 s | 15–30 s |
| Long-form, 3 minutes and over | 5 s | 30 s |

Rules:

1. **Frame one is worth looking at, and moves.** No logo sting, no blank title, no "In this video…", no "Hey guys". A static first frame gets scrolled past.
2. **The voice starts at once.** No silent build-up. In cinematic work an image or a sound can carry the hook, but something happens in the first second.
3. **Vertical: put the hook in words on screen.** Viewers read before they listen, and many watch muted.
4. **Open a gap; don't fill it.** "HTTPS uses TLS 1.3 with AEAD ciphers" closes the door. "The padlock icon doesn't mean what you think it means" opens it.
5. **The hook is a promise the climax keeps.** Write the payoff first, then the hook that sets it up. A hook the video never pays off is clickbait.

| Pattern | Shape | Use for |
|---|---|---|
| Contrarian | "Everything you've been told about X is wrong." | myths, science, "how it really works" |
| Outcome | "By the end you'll be able to X." | tutorials, concepts |
| Mystery | "In 1987, something impossible happened…" | stories, history |
| Stakes | "This one mistake costs you X every year." | money, health, practical advice |
| Question | "Why does X happen?", with the question on screen | education |
| Result first | Show the finished result, then how it was made | tutorials, product |
| Pattern interrupt | An unexpected image, hard cut or sound | short-form, music-led work |

**In Facet:** make the hook its own narration line (`l1`) with a short pause after it; `script_check` reports when the hook lands. Tie the first cut to that line (`lines: ["l1"]`) so the striking picture is on screen while the words are said: a `hero_title`, a `stat_card` with the surprising number, or the strongest `video` cut you have.

## Spoken pace

Plan narration at 130 to 150 words a minute. `script_check` assumes 150 unless you give it a pace.

| Pace | Words a minute | Use for |
|---|---|---|
| Conversational | 150 | most explainers and product videos |
| Technical | 130 | code, architecture, dense ideas |
| Contemplative | 120 | reflective or cinematic voice-over |
| Energetic | 180–200 | vertical short-form, promos |

Words that fit, with narration filling 85 to 100% of the running time:

| Length | At 150 | At 130 | At 180 |
|---|---|---|---|
| 15 s | 32–38 | 28–33 | 38–45 |
| 30 s | 65–75 | 55–65 | 75–90 |
| 60 s | 130–150 | 110–130 | 155–180 |
| 90 s | 195–225 | 165–195 | 230–270 |
| 2 min | 260–300 | 220–260 | 305–360 |
| 3 min | 380–450 | 330–390 | 460–540 |

- Stay within 10% of the target. At 20% over, cut sentences; never fix length by speeding up the voice.
- Per scene, allow 2 to 2.5 words a second at a documentary pace with pauses, 2.5 to 3 when energetic: a 5 s scene carries 10 to 12 words, a 6 s scene 12 to 15.
- Give the opening and closing scenes little narration so the pictures breathe.
- After synthesis, `narration_timing` holds the real length. If narration runs more than a second past the plan, cut a line and synthesise again, or extend the closing scene. Do not pad with dead air.

## Pauses

Pauses are the voice's punctuation. Set them per line with `pause_after_seconds`; a pause "before" a line is the pause after the one ahead of it.

| After | Pause |
|---|---|
| the hook | 0.3–0.6 s |
| a setup line or a list item | 0.2–0.4 s |
| the line before a reversal ("but…") | 0.4–0.7 s |
| the end of a section | 0.5–0.8 s |
| a surprising claim | 0.6–1.0 s |
| the key insight | 1–3 s |

Too many long pauses sound theatrical and slow. Voice direction is in `guidance/craft/voice-performance.md`.

## Visual pacing

Something on screen changes at a steady rhythm: a cut, a new element, a highlight, a camera move.

| Format | A change every | Typical scene or shot | Cuts a minute |
|---|---|---|---|
| Vertical short-form | 1–3 s; no static hold over 3 s | 1.5–4 s | 20–40 |
| Explainer | 3–5 s | 4–8 s a scene; up to 10–12 s for a diagram that builds | 8–15 |
| Product promo | 2–4 s | 2–5 s | 15–30 |
| Cinematic | varied on purpose | 4–8 s average; 2–4 s in action | 8–15 |
| Documentary | 4–8 s | 6–12 s | 5–10 |
| Montage on music | 1–3 s | on the beat or the bar | 20–40 |
| Talking head | a punch-in, cutaway or overlay every 5–10 s | the speaker's own sentences | – |

- **Breathe.** Vary shot length on purpose: long, medium, short, short, long. Never the same length three times in a row.
- **Alternate density.** A dense scene (chart, diagram, code) is followed by breathing room (a title, an image, a held frame).
- **Never three scenes of the same type in a row.** `plan_check` flags it, and flags slideshow risk.
- **Hold what matters.** A key number or insight stays 3 to 5 s; on-screen text stays long enough to read twice (2 to 4 s for a short block in vertical).
- **Cut on the words.** Visual changes land on the narration line that names them: give cuts `lines` from the `narration_timing` instead of guessed seconds.
- **Interrupt the pattern** every 45 to 90 s in longer videos (a breather, a change of scene type, a question), and signpost every 30 to 45 s ("here's where it gets strange").

## Pacing in the edit

For recorded speech (talking heads, interviews, podcasts):

- **Cut:** filler words ("um", "you know") at word boundaries; false starts, keeping the final take; repeated points, keeping the best delivery; tangents.
- **Trim dead air** over 1.5 s down to about 0.5 s. `silence_cutter` does this (`min_silence_duration`, `padding_seconds`); `mark` mode lists silences without cutting.
- **Keep** breath pauses of 0.3 to 0.8 s, deliberate emphasis pauses, and bridges like "So…" that carry the flow.
- **Join smoothly:** a J-cut (next audio starts about 0.5 s before the picture) or an L-cut (audio runs about 0.5 s past it); hard cuts at topic changes. Never cut mid-word.
- **Match the length:** under 60 s, cut hard; 1 to 10 minutes, keep natural pauses; over 10 minutes, let scenes breathe and cut only real problems.
- Speed up dull setup footage 1.2 to 1.5 times; never speed up speech.

Editing technique is in `guidance/craft/editing.md`.

## The ending

Land, don't trail. The call to action comes within the last 5 s and is one specific action. Nothing new is introduced in the landing. In short-form, let the last frame lead back into the first so a replay feels seamless.

## Quality checks

- `script_check`: word count within 10% of the target at the planned pace; the hook lands on time; no line too long to speak or caption.
- The first frame has visual interest and motion; the voice starts at once; no "In this video".
- The climax pays off the hook.
- Something changes every 3 to 5 s (1 to 3 in vertical); no static hold over 3 s in short-form.
- Shot lengths vary; no three same-type scenes in a row (`plan_check`).
- Pauses sit where the script directs; the key insight is followed by silence.
- `output_review` with `coverage`: no unplanned gaps in narration, no dead air at the start, and the video does not run on after the last line. A `contact_sheet` shows the visual rhythm at a glance.
