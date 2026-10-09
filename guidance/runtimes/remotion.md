# Remotion

Remotion renders React compositions frame by frame in a headless browser. In Facet it renders the stock compositions for templated work and the project's own entry for atelier work, both through `video_compose` with `render_runtime: "remotion"`. Choosing a runtime and a mode: `guidance/runtimes/composition-modes.md`.

## Choose it when

- The stock scenes carry the piece: titles, stat cards, KPI grids, bar, line and pie charts, callouts, comparisons, terminal and screenshot scenes (`Explainer`).
- Word-highlight captions belong in the picture.
- A presenter is on camera (`TalkingHead`), or clips are cut to music or narration with title cards (`CinematicRenderer`).
- Stills need motion: camera moves on images, `anime_scene` crossfades with particles.
- An atelier piece is easier to write in React than in HTML and GSAP: data-driven layouts, hand-drawn charts, 3D.

## Templated

`video_compose` takes `edit_decisions` with:

- the locked settings: `render_runtime`, `composition_mode` (`templated`), `renderer_family`, `delivery_promise`;
- `cuts` (or `scenes` for `CinematicRenderer`). Each cut has an `id` and either a scene `type` with its fields or a `source` (a media cut, `image` or `video`). Time it with `in_seconds` and `out_seconds`, or with `lines` that follow narration lines in the `timing_path`;
- `captions`, `audio`, `theme` (a style id), `width`, `height` (even numbers, 1920×1080 by default) and `fps` (30 by default).

An `Explainer` video ends at the last cut unless `duration_seconds` says otherwise. Fields of every composition and scene type are in `guidance/runtimes/scene-types.md`; how the editor builds the cuts is in `guidance/stages/edit.md`.

What makes templated renders look good:

1. **One background family a video,** from the style. Light-to-dark flashes between scenes look broken.
2. **Fields sit on the cut itself** (`text`, the chart data), never nested under another key.
3. **Give scenes time:** a chart needs at least 4 s to animate in and be read; a hero title about 4 s; most scenes 4 to 6 s, so 8 to 10 scenes in 45 to 50 s.
4. **One chart palette** for every chart in the video: the style's `chart_palette`.
5. **Numbers as people read them:** 8.1 with a " Billion" suffix, never a ten-digit raw value.
6. **Group and punctuate:** `section_title` cuts group scenes ("The crisis", "The data"); a `stat_reveal` lands one number hard.
7. **Words on screen come from the composition,** never from text baked into a generated image.

## Atelier

For hero work, write the composition for this piece; the method is in `guidance/runtimes/composition-modes.md`.

1. Write a project-local entry (`.tsx`) that registers your composition under an id (`guidance/vendor/remotion-best-practices/rules/compositions.md`). Keep every file of it inside the project.
2. Render with `video_compose`:

   ```json
   {"edit_decisions": {"render_runtime": "remotion", "composition_mode": "atelier",
     "delivery_promise": "motion_led",
     "bespoke": {"entry": "atelier/index.tsx", "composition_id": "launch-film"}}}
   ```

   `describe` with `video_compose` gives the exact `bespoke` fields.
3. Captions: draw them from the `words` file `subtitle_gen` writes, or burn them afterwards with `ffmpeg_caption_burn`.

Engine mechanics that bite:

- **All motion from the frame:** `useCurrentFrame()` with `interpolate()` or `spring()`. CSS animations, CSS transitions and Tailwind `animate-*` classes do not render. Clamp every `interpolate()` (`extrapolateLeft` and `extrapolateRight` set to `"clamp"`) so values never run past their ends.
- **Scene length:** inside a sequence, `useVideoConfig().durationInFrames` is the whole composition's length, not the scene's. Pass each scene its own duration and time crossfades and camera moves from that.
- **Determinism:** no `Math.random()` or `Date.now()` while rendering; use Remotion's `random(seed)`, or particles flicker from frame to frame.
- **GSAP only when the idea needs it** (per-letter type, shape morphs, motion along a path): a paused timeline seeked to `frame / fps`, never driven by `requestAnimationFrame`, plugins registered once at module level. If `interpolate()` or `spring()` does it in 20 lines, skip GSAP.
- **Fonts from local files only.** `@remotion/google-fonts` downloads while rendering; load a font file from the project instead (`guidance/vendor/remotion-best-practices/rules/fonts.md`, local fonts).
- **Render one at a time.** Each render runs its own browser; parallel renders can run out of memory.

## What Facet does for you

- **Templated:** every prop is checked before the first frame, and a bad one fails the render naming the cut and field; media fields (`source`, `image`, `images`, `video`, `logo`, `screenshot` and the others) are staged from project paths, absolute paths or URLs; the style's fonts are installed locally; `captions` draws word captions from the timing (`words_per_page`, `highlight_color`, `position`); `audio` plays the narration and lowers the music under it (`duck_under_narration`); loudness is normalised after the render.
- **Atelier:** media staging, the render, loudness normalisation and the review. Everything on screen is your composition's.
- **Render time:** `estimate` gives it. When it reports `prefer_shell`, run `facet tools run video_compose --input <file>` in the shell (in the background where the host allows).

## Before and after the render

- Before: every source exists; the narration is not longer than the video; the music covers the duration or loops. Total length is the sum of the scenes minus the transition overlaps.
- After: `media_probe` the output. It has a video and an audio stream, the planned size and fps, and a duration within 5% of the target. No audio stream means the sound never reached the composition: fix `audio` and render again.
- `output_review` with `contact_sheet` and `coverage` (`timing_path`, `captions_path`): frames, narration and caption coverage, gaps, loudness. Then `frame_sample` at scene midpoints for anything that looks wrong. A clean exit code is not a finished video.

## Vendor knowledge

- `guidance/vendor/remotion-best-practices/SKILL.md`: the Remotion rules: timing, sequencing, transitions, text animation, fonts, audio, measuring text, `calculateMetadata`.
- `guidance/runtimes/scene-types.md`: the stock compositions and scene types.
- `guidance/vendor/synthetic-screen-recording/SKILL.md`: terminal demos.
- `guidance/vendor/gsap-core/SKILL.md`, `guidance/vendor/gsap-timeline/SKILL.md`: GSAP, when an atelier scene needs it.
