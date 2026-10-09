# Stage: scene_plan

**Run by:** the director, with the art director. `plan_check` checks it; the operator approves it.
**Record:** `scene_plan`.

Words become pictures here: what is on screen at every moment, why, how it moves, and whether the configured tools can make it. A good script with a bad scene plan makes a confusing video.

## Inputs

`script`; the locked plan in `proposal_packet` (style, `render_runtime`, `composition_mode`, `renderer_family`, `delivery_promise`, taste profile); the chosen structure's suggested scenes for each beat; `capabilities` (via `pipeline_describe` for this stage); `source_media_review` for footage.

## Method

1. **Read every section:** the concept, its visual cues, its emotional beat, the time it has.
2. **Look for the visual idea.** How do the best creators show this? Which metaphor will the audience recognise instantly? What has nobody tried? What can the configured providers actually make?
3. **Break each section into one to three scenes.** For each scene state:
   - what is on screen and why the scene exists (its purpose: the information it carries or the feeling it causes);
   - its render form: an Explainer scene type (`stat_card`, `comparison`, `terminal_scene`…), an `image` or `video` cut, a source clip, or a scene of the atelier composition;
   - its timing, tied to the narration lines it covers;
   - its transitions, and overlays (titles, captions, labels, chips) listed separately from the picture;
   - its required assets and where each comes from: supplied, stock, generated, or rendered by the composition.
4. **Specify five aspects** for every shot that is filmed, found or generated: subject; subject motion; scene (setting, point of view, time of day); spatial framing (shot size, position, depth); camera (lens, height, angle, focus, steadiness, movement). For scenes rendered by the composition, mark the camera as N/A and describe the layout and which element holds the centre. Silent omission produces vague prompts.
5. **Hold the style and the taste profile.** Palette, type and transitions come from the style; holds respect its pacing rules. Visual variance sets how much layouts change; information density caps text and callouts; motion intensity sets transitions and camera.
6. **Apply the rules that keep videos honest and readable:**
   - Exact text (titles, numbers, names, figures, calls to action, legal lines) is a text scene rendered by the composition, never text inside a generated image.
   - A process or transformation moves on screen; a static picture of a dynamic idea fails.
   - Atelier work has no hero-component spine: each scene has its own primary subject, different from its neighbours; the signature device appears in one or two beats, not as scaffolding. Write the art direction and the per-scene plan before any composition code.
   - Captions and on-screen text do not repeat each other in the same moment.
7. **Check coverage:** the first scene starts at 0 and the last ends at the full duration; every script section and every visual cue is covered; no gap over one second unless it is a deliberate beat.
8. **Check variety:** never three scenes of the same type in a row; at least three types; high-information scenes alternate with breathing room; something changes on screen every three to five seconds (one to three in short-form).
9. **Check feasibility:** every asset has a configured, supplied or rendered source. Where a capability is unavailable, plan the alternative and say what changes. Mark paid generation so the asset stage can sample first.
10. **Guard against the slideshow.** Repeated layouts, decorative pictures with no purpose, motion without reason, scenes with no stated intent, and a run of text cards all make a video feel like animated slides. A motion-led promise needs real motion in most shots.
11. **Run `plan_check`** on the plan with the script's timing and the delivery promise: coverage, gaps and overlaps, variety, slideshow risk, promise fit and asset feasibility. Fix every failure; explain any warning you accept.

## The record

`scene_plan` (`artifacts/scene_plan.json`): the style, and scenes in order, each with id, type, description, start and end seconds, the script section it covers, framing, movement, transitions, overlay notes, shot language, shot intent, narrative and information role, whether it is a hero moment, and its required assets with their source.

## Review checklist

- The narration is covered end to end; no gaps or overlaps.
- No three same-type scenes in a row; at least three types.
- Every asset is feasible now, or supplied.
- Style and taste profile followed; exact text is never generated inside images.
- `plan_check` passes coverage, variety, slideshow risk and delivery promise.

## Approval

Always. Present the plan as a readable scene list (time, what is seen, source) with the `plan_check` summary, then stop and end the turn.

## Tools

`plan_check`; planning: `pipeline_describe`, `capabilities`, `describe`, `guidance`. For footage: `frame_sample`, `scene_detect`.
Craft: `guidance/craft/visual-planning.md`, `guidance/craft/data-visualization.md`, `guidance/craft/typography.md`, `guidance/craft/broll-and-stock.md`, `guidance/craft/image-generation.md`, `guidance/craft/video-generation-prompting.md`, `guidance/craft/taste-direction.md`.
Runtimes: `guidance/runtimes/scene-types.md`, `guidance/runtimes/composition-modes.md`.
