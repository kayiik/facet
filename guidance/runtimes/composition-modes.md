# Composition modes and runtimes

Four settings decide how a video is built. The director proposes them at the `proposal` stage, the operator approves them, and `edit_decisions` carries them unchanged to the render. Changing one later is a change of plan: ask first.

| Setting | Values | Decides |
|---|---|---|
| `render_runtime` | `remotion`, `hyperframes`, `ffmpeg` | the engine |
| `composition_mode` | `templated`, `atelier` | stock scenes, or a composition written for this piece |
| `renderer_family` | eight families, below | which stock composition renders templated work |
| `delivery_promise` | eight promises, below | what kind of video was promised; checked before rendering |

## Choosing the runtime

`capabilities` shows which runtimes are installed; `hyperframes_compose` with `operation: "doctor"` names what HyperFrames is missing.

| The brief needs | Runtime | Why |
|---|---|---|
| Stock scenes: titles, stat cards, charts, callouts, terminal and screenshot scenes | `remotion` | `Explainer` has them; rebuilding them in HTML costs time |
| Word-highlight captions drawn in the picture | `remotion` | the composer's caption layer |
| A presenter on camera | `remotion` | `TalkingHead` |
| Clips cut to music or narration: trailer, documentary montage | `remotion` | `CinematicRenderer` |
| Kinetic typography, heavy text motion, GSAP choreography | `hyperframes` | its natural medium; in React the same motion is slow to write and fragile |
| Product promo, launch reel, designed title card | `hyperframes` | CSS and GSAP layouts; registry blocks give a start |
| A website or interface turned into video | `hyperframes` | the website-to-video workflow |
| A registry block: data chart, grain overlay, shader transition | `hyperframes` | the registry is HyperFrames only |
| A music piece cut on the beat | `hyperframes` | the music-to-video workflow |
| A synthetic terminal or interface demo | either | `terminal_scene` for terminals; HyperFrames for other interface chrome |
| Cutting and joining footage, no composed graphics | `ffmpeg` | a composition engine adds nothing |
| The chosen runtime is not installed | stop | ask; never substitute another |

Don't default to Remotion for motion graphics that HTML and GSAP express better, or to HyperFrames for briefs the stock scenes already cover.

## Present both

When Remotion and HyperFrames are both installed, present both before locking `render_runtime`, whatever the pipeline usually uses:

1. For each, one sentence on what it does best for this brief and one honest trade-off. Add FFmpeg when the brief is footage cutting.
2. Your recommendation, tied to the delivery promise and the visual approach.
3. Wait for the choice. Record the options and the choice in the `proposal_packet`.

> **Remotion:** your three growth numbers become stock stat cards and a line chart, with word captions. Trade-off: stock motion; it looks like a good explainer, not a launch film.
>
> **HyperFrames:** hand-built kinetic type and transitions around the product shots. Trade-off: every scene is written from scratch, so it takes longer and needs more review rounds.
>
> I recommend HyperFrames: the promise is a motion-led launch, and three numbers are easy to animate by hand.

When only one is installed, say so: "HyperFrames is not installed here, so I'll use Remotion."

## Motion-required briefs

Trailers, teasers, hype edits, launch reels and every `motion_led` promise depend on real motion:

- Confirm the runtime works before the proposal is approved.
- Never turn the piece into stills with camera moves, an animatic or a slideshow, or drop to FFmpeg when that turns moving shots into stills, without the operator's agreement.
- Never swap runtimes silently: if HyperFrames was chosen and fails, don't render with Remotion instead.
- When a runtime, a render or clip generation fails, stop and report: what was attempted, what failed, whether it is setup, provider access, a tool fault or the design, the options, and your recommendation.
- Spend nothing on a downgraded version unless the operator accepts it as an animatic or proof of concept.

## Templated or atelier

| | Templated | Atelier |
|---|---|---|
| What | stock scene types in stock compositions | a composition hand-authored for this piece |
| Gives | speed, reliability, repeatability | a look no other video has |
| Costs | videos look alike | more time, iteration and review |
| Right for | batches, language variants, drafts, internal clips | hero work: marketing, launches, brand pieces, an explainer that must impress |
| Rendered by | `video_compose` with `cuts` (or `scenes`) and a `renderer_family` | `video_compose` with `bespoke` (Remotion), or a HyperFrames workspace |

Recommend atelier for hero work and say what it costs, so the operator opts in knowingly; when hero work stays templated, record why. HyperFrames has no stock scene catalogue, so `hyperframes` always goes with `atelier`. Remotion does both.

## Atelier: how to build it

Reuse engine knowledge, never creative components: how the engine seeks a timeline is knowledge; how an earlier video looked is not.

1. **Art direction for this subject.** The taste profile (`guidance/craft/taste-direction.md`), then palette, type, motion character, layout system and one signature device (`guidance/vendor/visual-style/SKILL.md`), written into the project before any code. If it resembles an earlier piece, keep looking.
2. **Each scene its own composition:** a primary subject different from its neighbours', a job no other beat does, a different layout, scale or motion register. The signature device appears in one or two beats, not in every scene. Two scenes with the same primary subject: re-plan.
3. **Motion from principles:** anticipation, follow-through, slow in and out, arcs; easing as emotion, timing as weight (`guidance/vendor/hyperframes-creative/references/motion-principles.md`). At least three different easings across the piece.
4. **No frozen looks:** no stock scene types, registry blocks used as whole scenes, or components from earlier projects. A block can be an ingredient (a grain overlay, a transition), never the scene.
5. **Captions or on-screen text, one role:** when a scene shows the words being spoken, don't caption them too.
6. **Render** through the runtime's atelier path (`guidance/runtimes/remotion.md`, `guidance/runtimes/hyperframes.md`).
7. **Distinctness review** before the final render: could this be any other product's video? Does it reuse an earlier look? If so, back to step 1.

## Renderer families (templated)

| `renderer_family` | Composition | For |
|---|---|---|
| `explainer-data` | `Explainer` | numbers and charts carry the argument |
| `explainer-teacher` | `Explainer` | a concept built step by step |
| `product-reveal` | `Explainer` | product shots, features, a call to action |
| `screen-demo` | `Explainer` | recordings as `video` cuts, `terminal_scene`, `screenshot_scene` |
| `animation-first` | `Explainer` | illustrated or generated stills under camera moves, `anime_scene` |
| `cinematic-trailer` | `CinematicRenderer` | clips cut to music, title cards |
| `documentary-montage` | `CinematicRenderer` | footage-led montage under narration |
| `presenter` | `TalkingHead` | a presenter on camera with captions and overlays |

Fields of every composition and scene type: `guidance/runtimes/scene-types.md`.

## Delivery promises

| `delivery_promise` | Holds when |
|---|---|
| `motion_led` | footage, generated clips or animated scenes carry most of the running time |
| `source_led` | the operator's material leads; stock or generated media only fills gaps |
| `data_explainer` | chart and stat scenes sit at the claims, each with its source |
| `teacher_explainer` | each step of the idea has its own visual |
| `screen_demo` | the product is shown working at every step the narration describes |
| `avatar_presenter` | a presenter is on screen throughout; `avatar` is unavailable, so the operator supplies the recording or the promise changes |
| `hybrid` | the planned mix of sources holds |
| `localization` | timing, captions and on-screen text follow the new narration |

`plan_check` checks the scene plan against the promise and flags slideshow risk before anything renders.

## What Facet does for you

Props are validated before the first frame (an unknown field fails the render, naming the cut and field); media is staged from project paths, absolute paths or URLs; the style's fonts are installed locally, so nothing is fetched at render time; `captions` with a `timing_path` draws word captions; music is lowered under the narration (`duck_under_narration`); loudness is normalised after every render; `output_review` adds a contact sheet and narration and caption coverage. Each runtime page says which of these apply there.
