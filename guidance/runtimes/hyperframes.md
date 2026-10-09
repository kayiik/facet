# HyperFrames

HyperFrames renders video from HTML: a composition is a page whose elements carry their timing in data attributes and whose motion is a paused GSAP timeline that the renderer seeks frame by frame. There is no stock scene catalogue, so every HyperFrames composition is written for its piece (`composition_mode` `atelier`). Choosing a runtime and a mode: `guidance/runtimes/composition-modes.md`.

## Choose it when

- Kinetic typography, heavy text motion, GSAP choreography.
- Product promos, launch reels, designed title cards.
- A website or interface turned into video (`guidance/vendor/website-to-video/SKILL.md`).
- A registry block helps: a data chart, a grain overlay, a shader transition.
- A short unnarrated motion graphic: lower third, stat hit, logo sting (`guidance/vendor/motion-graphics/SKILL.md`).
- A music piece cut on the beat (`guidance/vendor/music-to-video/SKILL.md`).

Stay with Remotion for the stock scenes and charts, the word-caption layer, `TalkingHead` and `CinematicRenderer`; none of them exists here. A scene written as React belongs in Remotion. The vendor router calls HyperFrames the default for every video; in Facet the runtime is chosen at the proposal, with both runtimes presented.

## The workspace

One workspace per project, inside the project (for example `projects/<name>/hyperframes`), never shared: HyperFrames resolves sub-compositions, media and blocks relative to it.

1. **`doctor`:** Node, the HyperFrames CLI installed with Facet, and FFmpeg must all be found. If not, stop and report; don't switch runtime.
2. **`scaffold_workspace`** with `workspace_path`: writes hyperframes.json (registry and install paths), a skeleton index.html (a 10-second 1920×1080 root that loads GSAP from a CDN, so rendering needs the network) and the assets and compositions folders. Set the root's duration to the narration's length and its size to the delivery size.
3. **Write the composition** by hand (`guidance/vendor/hyperframes-core/SKILL.md`). Copy the media it uses into the workspace's assets folder and reference it by relative path.
4. **`add_block`** with `block_name` installs a registry block (`guidance/vendor/hyperframes-registry/SKILL.md`). A block is an ingredient, never a whole scene.
5. **`lint`, `validate`, `inspect`** (or `check`) until clean.
6. **Render:** `video_compose` with `render_runtime: "hyperframes"` and the workspace (`describe` names the field), or `hyperframes_compose` with `operation: "render_existing"`, `workspace_path` and `output_path`.

The vendor guides write commands as `npx hyperframes …`. In Facet, use the operations below: they run the HyperFrames version installed with Facet, not whatever npx would fetch. Don't run `hyperframes init`, which builds a project of its own; and use Facet's voice and caption tools rather than the CLI's own speech, transcription and background-removal commands.

## Operations

| `operation` | Does |
|---|---|
| `doctor` | whether Node, the HyperFrames CLI and FFmpeg are available, and where |
| `scaffold_workspace` | creates the workspace files; needs no runtime |
| `add_block` | installs `block_name` from the registry; needs the network |
| `lint` | static contract checks: missing composition ids, duplicate ids, overlapping tracks, unregistered timelines |
| `validate` | loads the page in a headless browser: runtime errors, timeline registration, WCAG contrast |
| `inspect` | seeks through the timeline: text spilling out of its box or off the canvas |
| `check` | the combined gate: lint, runtime, layout, motion and contrast in one report |
| `render`, `render_existing` | render the workspace as authored to `output_path` (default: renders/final.mp4 inside the workspace) at standard quality, 30 fps |

The checks return the CLI's own JSON report. Lint and validate must pass before any render; never render a failing composition. The checks look at each composition on its own, so a sub-composition that fails to mount, or a video or audio element inside a sub-composition (it renders blank), passes them. After the render, look at frames from every scene (`frame_sample`, or the contact sheet from `output_review`).

## Authoring rules that bite

- Every timed element has a start, a duration, a track index, the clip class and a stable id; the root declares its composition id, start, duration, width and height. Details in `guidance/vendor/hyperframes-core/SKILL.md`.
- One paused GSAP timeline per composition, registered synchronously when the page loads; nothing built in timers, promises or async callbacks.
- Bounded repeats only: an infinite repeat (`repeat: -1`) breaks the seek-and-capture render. No `Math.random()` or `Date.now()` in motion.
- Video and audio elements sit directly in the root composition, never inside sub-compositions.
- Text over footage: keep the video visible but dimmed (about brightness 0.55, saturation 0.85), put a local scrim behind the text block and a soft shadow on the text. Lowering the video's opacity muddies both layers.
- Footage must fill the frame. Free stock clips are often 640×360; fitted with padding, they render as a small box in black. `media_probe` the sources at the assets stage and prepare them with `source_edit` and `fit: "cover"`; when no sharp source exists, change the plan rather than ship the letterbox.
- Motion vocabulary: two to four motion rules a beat and at least three easings across the piece (`guidance/vendor/hyperframes-animation/SKILL.md`; GSAP itself in `guidance/vendor/gsap-core/SKILL.md` and `guidance/vendor/gsap-timeline/SKILL.md`).

## Fonts

HyperFrames embeds a fixed set of families offline, Inter, Playfair Display and JetBrains Mono among them (`guidance/vendor/hyperframes-creative/references/typography.md`). Any other family, Space Grotesk and Noto Sans included, needs an @font-face rule pointing at a font file in the workspace; otherwise the renderer fetches it from Google Fonts while building, which needs the network and raises a lint warning. Ask only for weights the family ships.

## Style, sound and captions

- **Style:** carry the style into CSS custom properties on the root: colours from `visual_language`, fonts from `typography`, easing and durations from `motion`. The style file stays the reference; if the workspace drifts from it, the style wins.
- **Sound:** narration and music go in the root as audio elements with their own start, duration and volume; music low, lower still under the narration. Or mix first with `audio_mix` (narration, music lowered under it, effect cues, `loudness_target`) and place the one mixed track.
- **Captions:** word spans built from the `words` file `subtitle_gen` writes, a registry captions block, or burned afterwards with `ffmpeg_caption_burn` (`guidance/craft/captions.md`; caption craft for HyperFrames in `guidance/vendor/hyperframes-media/SKILL.md`).
- **After the render:** through `video_compose`, loudness is normalised like any other render; then `output_review`.

## Vendor knowledge

- `guidance/vendor/hyperframes/SKILL.md`: the router and capability map.
- `guidance/vendor/hyperframes-core/SKILL.md`: the composition contract.
- `guidance/vendor/hyperframes-animation/SKILL.md`: motion rules, scene blueprints, transitions.
- `guidance/vendor/hyperframes-creative/SKILL.md`: palette, type, narration and beat planning.
- `guidance/vendor/hyperframes-media/SKILL.md`: captions, music and effects inside a composition.
- `guidance/vendor/hyperframes-registry/SKILL.md`: blocks and how to wire them in.
- `guidance/vendor/hyperframes-cli/SKILL.md`: what each check reports.
- Workflows: `guidance/vendor/website-to-video/SKILL.md`, `guidance/vendor/music-to-video/SKILL.md`, `guidance/vendor/motion-graphics/SKILL.md`.
