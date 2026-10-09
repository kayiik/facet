# Storytelling

The story is the order in which the audience learns things. Settle it before any visual. The writer owns it in `script`, the director checks it in `proposal`, and the critic tests it in `review`.

## Start from one sentence

1. Write the core message as one sentence a viewer could repeat to a friend. If you cannot, the brief is not ready: go back to `intake`.
2. Name the audience, what they believe now, and what they should know, feel or do at the end. The distance between the two is the story.
3. List the key points. Each must move the viewer along that distance. A point that does not is cut, however interesting.

## Choose a structure

Every pipeline lists its structures (`pipeline_describe`): beats, each with a share of the running time, a purpose and the scene types that suit it. Pick the one whose "use when" matches the brief and record the choice in the `proposal_packet` and the `script`.

- Map each key point to one beat. A beat with nothing to carry merges into its neighbour; a point with no beat is cut or the structure is wrong.
- Give each section the running time its beat's share implies. `script_check` reports each section's share against the structure; fix sections that are far off.
- Keep one idea per scene and one new concept per 30 to 45 seconds.

## The explainer arc

The default shape for an explanation. Timings are for three minutes; scale by the shares.

| Beat | At 3 min | Share | Job |
|---|---|---|---|
| Hook | 0:00–0:08 | 4% | Pattern interrupt or counterintuitive claim, one or two sentences. Striking image. |
| Tension | 0:08–0:30 | 12% | What most people think, why it is wrong or incomplete, and why it matters. |
| Foundation | 0:30–0:50 | 11% | The simplest building block. One idea, one visual. Ends on "but" or "therefore". |
| Complication | 0:50–1:15 | 14% | The wrinkle. Evolve the previous visual rather than replacing it. |
| Breather | 1:15–1:20 | 3% | A held frame, a visual joke, a beat to let it sink in. |
| Key insight | 1:20–1:50 | 17% | The aha. The most polished visual of the video, then 1 to 3 seconds of silence. |
| Proof | 1:50–2:20 | 17% | One concrete case: "watch what happens when…". |
| So what | 2:20–2:45 | 14% | Back to the real world, from the specific to the general. |
| Close | 2:45–3:00 | 8% | Call back the hook, restate the insight in one sentence, one specific action. |

| Length | Concepts | Hook | Tension | Core | Proof | Close |
|---|---|---|---|---|---|---|
| 1 min | 1–2 | 5 s | 10 s | 30 s | 10 s | 5 s |
| 2 min | 2–3 | 8 s | 15 s | 60 s | 25 s | 12 s |
| 3 min | 3–5 | 8 s | 22 s | 105 s | 30 s | 15 s |
| 5 min | 5–8 | 10 s | 30 s | 190 s | 50 s | 20 s |

The hook beat may run several seconds, but the hook itself lands in the first 2 to 5 seconds (1 to 2 in vertical short-form). Hook and stakes are done by second 30; that is where most viewers leave. Hook patterns and pacing numbers are in `guidance/craft/hooks-and-pacing.md`.

## Join beats with "but" and "therefore"

Never "and then". "And then" lists facts; "but" and "therefore" make each beat the cause of the next.

- Weak: "Atoms have electrons, and then the electrons have energy levels, and then…"
- Strong: "Atoms have electrons, but they don't orbit like tiny planets, therefore we need a different model…"

The pattern: what you think you know, **but** why that is incomplete, **therefore** the real mechanism, **but** the new puzzle it creates, **therefore** the answer, **therefore** what changes for you.

## Misconception first

For science, technical and "how it really works" topics, open with what the audience believes, then show why it fails. Stating the misconception creates the gap, and correcting it on screen makes the right idea stick better than stating it plainly.

## Guided discovery

Don't hand over the answer; rebuild the reasoning so the viewer feels they found it.

1. **The question:** specific and concrete.
2. **The naive attempt:** the obvious approach works partly, then breaks.
3. **The key insight:** one new idea, then a pause.
4. **The build:** apply it step by step; each step feels inevitable.
5. **The generalisation:** "this works beyond our example…".

Reveal progressively: never show the full picture at once. Each layer arrives when the narration names it. In Facet, tie cuts to narration lines (`lines: ["l3","l4"]` in `edit_decisions`) so the visual lands with the words that need it.

## Other shapes

- **Product:** a problem the viewer recognises, the product as the turn, proof (it working, one concrete result), one action. Show the product within the first third.
- **Cinematic:** tension, reveal, landing. A beat map with sparse narration; the reveal is a visual event, and the landing holds long enough to feel.
- **Editorial** (talking head, interview, documentary): the speaker's own arc. Question, evidence, turn, meaning. Cut for clarity, never stitch words into a claim the speaker did not make.
- **Animation:** establish the world and the character, show what they want and what blocks it, end on the change.
- **Data:** the number, compared with what, why it moved, what it means. See `guidance/craft/data-visualization.md`.

## Write causes, not feelings

Beats and directions name what the audience sees and hears, not the emotion you hope for. Adjectives do not constrain pixels; causes do.

| Avoid | Write instead |
|---|---|
| epic reveal | wide aerial pull-back; the figure silhouetted against the rising sun |
| inspiring moment | low angle on the face; light catches the edge of a tear |
| moody atmosphere | low-key light, deep shadows, haze in the background |
| powerful music swell | music drops out at 0:42, 1.5 s of silence, returns at half tempo with low drums |

This holds for narration directions, beat descriptions and every field the director reads in the `scene_plan`.

## Name subject changes

When a beat brings in a subject, removes one, or moves focus, say so:

- **revealing:** a subject enters or is uncovered (door opens, the camera pans to find them, fog clears);
- **disappearing:** a subject leaves or is removed (walks out, fades, is eclipsed);
- **switching:** focus jumps from one subject to another (cut, rack focus, whip pan);
- **alternating:** several subjects trade focus within a beat (a debate, an ensemble).

Add one or two sentences on the mechanism (cut, pan, reveal by light). The director turns it into a transition.

## One line of camera intent per beat

Attach one concrete line so the director does not start from nothing. No mood words.

```text
[0:30] Foundation: atoms aren't tiny planets
Narration: "We grew up picturing electrons as tiny planets circling the nucleus…"
Camera intent: medium shot of a stylised atom; slow rotation; everything in focus.
```

## Rules for explanations

- **Segment:** one new concept per 30 to 45 seconds; a three-minute video carries three to five.
- **Signpost** every 30 to 45 seconds: "here's where it gets strange".
- **Show while you say:** the visual for a sentence is on screen while the sentence is spoken, not seconds later.
- **Cut seductive detail:** interesting but irrelevant facts cost understanding.
- **Say it and show it:** narration plus pictures, not paragraphs on screen. On-screen text is labels, key numbers and short emphasis.
- **Silence after the key insight:** 1 to 3 seconds, set as `pause_after_seconds` on that narration line.
- **Pattern interrupt** every 45 to 90 seconds in longer videos: a breather, a change of scene type, a question.

## Quality checks

A critic can apply these to a script, a plan or a cut:

- The core message fits in one sentence and matches the `brief`.
- The hook lands in the first 2 to 5 seconds; hook and stakes are done by 30 seconds.
- There is a turn, the climax pays off the hook, and the close calls it back with one specific action.
- No section joins with "and then"; each follows from the one before.
- The concept count fits the length table; no beat is there only to fill time.
- Directions describe visual and sound causes; subject changes are named.
- Every factual claim traces to the `research_brief`.
- `script_check`: sections sit near their beat shares and the hook lands on time.
- `plan_check`: every narration line is covered by a scene, with no gaps.
