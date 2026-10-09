# Stage: script

**Run by:** the writer. The critic reviews it; the operator approves it.
**Records:** `script`; the critic's `review` of the script.

The script is the backbone: every scene, sound and caption follows from it. A weak script cannot be saved by good visuals.

## Inputs

The selected concept and plan in `proposal_packet` (or the `brief` where the pipeline has no proposal stage), `research_brief`, the chosen structure from the pipeline, the style, and the voice plan. Footage-led work also reads `source_media_review` and the transcript.

## Method

1. **Take the brief from the concept:** target duration (it sets the word count), hook, key points, core message, tone, audience, structure.
2. **Lay out the structure.** Give each beat its share of the running time and map each key point to one beat. Each beat must earn its place: if two beats can merge without loss, merge them.
3. **Count the words.** At 150 words a minute (conversational): 30 s is about 65 to 75 words, 60 s about 130 to 150, 90 s about 195 to 225, 120 s about 260 to 300. Contemplative or technical reads run 120 to 130 a minute; energetic short-form 180 to 200. Narration at 85 to 90% of the running time leaves the opening and the close room to breathe. Twenty percent over the count means the voice rushes or the video overruns: cut.
4. **Write for the ear.** Short sentences, light contractions, clear punctuation, one idea a sentence. Join sections with "but" and "therefore", never "and then". The hook lands in the first seconds and opens a gap; the landing adds nothing new and ends on one specific action. Never open with "In this video…" or "Hey guys".
5. **Describe causes, not feelings.** "Wide aerial pull-back, the figure silhouetted against the sunrise", not "epic reveal"; "music drops out for 1.5 s, then returns at half tempo", not "powerful swell". Name subject changes (a subject is revealed, leaves, or focus switches) and the mechanism (cut, pan, light).
6. **Give every section a visual cue,** at least one every 8 to 10 seconds in narrated work: overlay, diagram, stat, animation, code, footage. Add one concrete line of camera intent where it matters.
7. **Plan the voice.** A voice performance plan: intent, pacing profile, energy curve, pause policy, and the sample section (the most performance-sensitive one, not automatically the first). Each narrated section carries at least two concrete delivery cues: pace, energy, emphasis words, pause before or after, a delivery note, or provider text with punctuation for the read. "Read naturally" is not a direction.
8. **Write the narration as lines.** One sentence or phrase a line, each with an id and the pause after it: short after setup lines, longer before a reversal, one to three seconds after the key insight. These lines go to the voice tool as `lines`, and the `narration_timing` it writes drives the cuts and the captions. Keep lines short enough to caption (two lines of at most 42 characters).
9. **Guide pronunciation** of technical terms, acronyms and names.
10. **Keep it true.** Every factual claim traces to the `research_brief`. Verify anything new and add its source; never invent a statistic, date or attribution.
11. **Check it.** Run `script_check` with the script, the target duration and pace, and the chosen structure (`describe` gives its request). Fix a word count more than 10% off target, a hook that lands late, lines too long to speak or caption, and sections far from their beat's share.
12. **Storytelling pass and critique.** Does the hook land in the first seconds? Is there a turn? Does every beat earn its place? Does the word count fit? How should it be voiced? Then hand it to the critic (`facet-critic`, or yourself from `guidance/stages/review.md`) and fix critical findings.

## Variants

- **Footage-led:** the script is built from the transcript. Each section points to its source passage (start and end in the source) and says what is kept, what is cut and why. Keep the speaker's meaning and the context each statement needs; never stitch words into a claim the speaker did not make. Without a transcript (the `transcription` capability may be unavailable), ask for one before writing.
- **Visual-led** (cinematic, montage, music-driven): the script is a beat map: each beat with its timing, what the audience should see and hear, sparse on-screen text, sound cues, and the reveal and landing. Narration only where it earns its place.
- **Short-form:** hook in the first one to two seconds, a visual change every one to three seconds, 35 to 40 words for 15 s, 70 to 80 for 30 s, 125 to 150 for 60 s.

## The record

`script` (`artifacts/script.json`): title, total duration, the voice performance plan, and sections, each with id, label, text, start and end seconds, speaker directions, delivery cues, visual cues, pronunciation guides and, for footage, the source reference.

## Review checklist

- Word count within 10% of the target at the planned pace (`script_check`).
- The hook lands in the first seconds; the climax pays it off; there is a turn.
- Every section has a visual cue and concrete delivery cues.
- Every claim is sourced; the call to action is specific.
- For footage: every passage exists in the source and keeps its meaning.

## Approval

Always. Present the script with the `script_check` summary and the critic's findings, then stop and end the turn.

## Tools

`script_check`; `describe` for its request; `guidance`.
Craft: `guidance/craft/storytelling.md`, `guidance/craft/hooks-and-pacing.md`, `guidance/craft/voice-performance.md`, `guidance/craft/short-form.md`, `guidance/craft/long-form.md`.
