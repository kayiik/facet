# Stage: proposal

**Run by:** the director, with the producer. The producer presents to the operator and owns the approval and the money. The critic reviews the concepts before the operator sees them.
**Records:** `proposal_packet`; the critic's `review` of the concepts.

This is where research becomes ideas the operator can choose between, and where the production plan is agreed. It is the last point where changing direction costs nothing.

## Inputs

`brief`, `research_brief` (or `source_media_review`, `video_analysis_brief`); the pipeline's structures, styles, runtimes, modes, families and delivery promises (`pipeline_describe` with this stage, which also reports live provider availability); `capabilities` for the full menu.

## Method

1. **Absorb the research.** Read the research summary first, then the angles, the surprising data points, the misconceptions, the gaps and anything timely.
2. **Know what can be made.** Check `capabilities` before designing anything. Do not propose what the configured providers cannot make. When one key or install would unlock something the brief needs, offer it, with what it would change; if the operator declines, design for what exists.
3. **Mood board, when the direction is open:** three to five reference images, two or three palette directions, a tone reference ("think a calm museum film, not a launch hype reel"), one or two music moods. Ask whether it feels right before writing concepts. A redirect here saves a whole concept round.
4. **Write two to four concepts, three by default.** Each one has:
   - a title and a hook under 20 words that opens an information gap and is grounded in a named finding (patterns that help: a surprising number, a misconception flipped, something that just changed, a question everyone has, a contrast, the thing nobody explains; write the hook in your own words first, then sharpen it). Never "In this video we'll…";
   - a structure, from the pipeline's structures or a narrative form the research supports (myth-busting when misconceptions are strong, data narrative when numbers surprise, journey when the topic needs progressive reveal, analogy for a lay audience, problem and solution, comparison, timeline, debate, tutorial, story);
   - a treatment and a style: how it will look, sound and move. Design the visual identity for this subject (palette from the subject, weight and motion from audience and tone); use a preset style only when it honestly fits;
   - the effect on the audience: what they should understand, feel or do, in the stance's terms;
   - duration and platform, and why it works, citing the research.
5. **Diversity gate.** Structurally: no two concepts share a structure or hook pattern, and each rests on different findings. Conceptually: each offers a different insight; one takes a creative risk; without their titles they would still be told apart. Three titles for one idea is one concept.
6. **Critic review.** Hand the concepts to the critic (`facet-critic`, or review them yourself against `guidance/stages/review.md`). Fix critical findings before presenting.
7. **Present progressively:** the research in two or three sentences; the concepts (title, hook, why it works, how it looks, length) with your recommendation; an invitation to mix ("the hook of A with the look of C"); then the production plan for the chosen one.
8. **Agree the production plan** (below). Then stop and end the turn.

## The production plan

- **Delivery promise** (`delivery_promise`): what kind of video is promised, whether real motion is required, whether supplied source must lead, the quality floor, and any approved fallback (none unless agreed). It is checked before rendering, so a still slideshow cannot pass for a motion-led video.
- **Renderer family** (`renderer_family`) for templated work, from the pipeline's list.
- **Engine** (`render_runtime`). When both Remotion and HyperFrames are installed, present both: for each, one line on why it fits this brief and one honest trade-off, then your recommendation tied to the delivery promise and the visual approach, and wait for the choice. When only one is installed, say so ("HyperFrames is not installed here; I'll use Remotion"). When the pipeline's runtimes rule one out, say that too, and why. Remotion suits the Explainer scenes, data charts, word captions and the `TalkingHead` and `CinematicRenderer` compositions. HyperFrames suits kinetic typography, HTML and GSAP motion graphics, product promos, launch reels, website-to-video and beat-timed music pieces. FFmpeg suits pure cutting and joining of footage; as a stand-in for animation it is a downgrade.
- **Composition mode** (`composition_mode`). Templated assembles the stock scene types into the stock compositions: fast and reliable, and the reason videos look alike; right for batches, language variants, drafts and internal clips. Atelier hand-authors a composition for this piece and is the recommendation for hero work (marketing, launches, brand pieces, an explainer that must impress); it costs more time and iteration, so say so. Atelier starts from a written art direction (palette, type, motion character, layout system, one signature device used in one or two beats) and plans each scene as its own composition. HyperFrames work is atelier by nature.
- **Taste profile:** a design read tied to audience, promise and platform ("investor-facing launch: precise, restrained, credible", never just "modern and clean"); three dials from 1 to 10 (visual variance, motion intensity, information density); the reference strategy; anti-patterns to avoid.
- **Voice:** provider and voice, why it fits, delivery style and pace; a sample before the batch. `edge_tts` (needs the network) and `piper_tts` (offline) are free; `openai_tts` and `elevenlabs_tts` are paid.
- **Music plan,** mandatory whenever the video has sound: a track from the operator's library (`music_library`), a track they will provide, or none. If there is no source, say so now, not at the asset stage.
- **Sources for each scene family:** supplied material; stock (`wikimedia`, `pexels_video`, `pixabay_video`, `direct_clip_search`); generated images or video; Explainer scenes that need no assets.
- **Paid steps:** for each, the tool, provider, model, why, sample then batch, and its credit note ("uses your OpenAI API credits"). No price unless the provider publishes one per call. Put the free alternative beside it.
- **Paths:** the free path in full, and what each paid upgrade changes in the result.

## Reference-driven work

Each concept names at least one element it keeps from the reference (pacing, structure, tone, hook style) and at least one it changes (angle, subject, treatment, platform), with why the change is better. A carbon copy is refused. After approval, make a 10 to 15 second sample (the hook and one middle scene, with the real voice, style and music) and get it approved before the script.

## The record

`proposal_packet` (`artifacts/proposal_packet.json`): the concept options with their grounding; the selected concept with rationale and modifications; the production plan above; the approval with the operator's notes.

## Review checklist

- Concepts differ in structure, hook and insight, and each is grounded in research.
- The plan uses only configured providers and names the free path.
- Every paid step has tool, provider, model and credit note.
- Both runtimes were presented when both are installed; runtime and mode are locked with a reason.
- Delivery promise, renderer family, voice and music are explicit.
- Hero work recommends atelier, or records why templated is right.

## Approval

Always. Present, stop, end the turn. Record the operator's answer in the packet. An earlier "go ahead" does not cover this gate.

## Tools

Planning: `capabilities`, `pipeline_describe`, `describe`, `estimate`, `guidance`. Media: `music_library` to list the operator's tracks.
Craft: `guidance/craft/taste-direction.md`, `guidance/craft/storytelling.md`, `guidance/craft/hooks-and-pacing.md`, `guidance/craft/visual-planning.md`.
Runtimes: `guidance/runtimes/composition-modes.md`, `guidance/runtimes/remotion.md`, `guidance/runtimes/hyperframes.md`, `guidance/runtimes/scene-types.md`. Art direction: `guidance/vendor/visual-style/SKILL.md`.
