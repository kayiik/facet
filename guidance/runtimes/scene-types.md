# Scene types and compositions (the composer's contract)

This is the exact contract of Facet's Remotion composer. `video_compose` sends these props; the composer checks every field before the first frame renders. An unknown composition, cut type or field, a blank required text, or bad timing fails the render with a message that names the cut and the field. Nothing renders as an empty frame.

## Compositions

| Composition | Use it for | Required props | Length |
|---|---|---|---|
| `Explainer` | Explainers, teasers, data stories, screen demos, kinetic text: a timeline of scene cuts | `cuts` | `duration_seconds`, or the last cut's `out_seconds` |
| `CinematicRenderer` | Trailers, brand films, documentary montage: graded video scenes and title plates | `scenes` | `duration_seconds`, or the last scene's end |
| `TalkingHead` | A supplied presenter video with word captions and overlays (portrait by default) | `videoSrc`, `captions`, `duration_seconds` | `duration_seconds` |
| `TitledVideo` | A finished clip with an editorial tagline over it | `videoSrc`, `tagline`, `taglineInSeconds`, `duration_seconds` | `duration_seconds` |
| `ProductReveal` / `ProductRevealVertical` | An 8-second product reveal from one product image | `productImage`, `productName`, `price`, `tagline`, `closer` | 8 s, or `duration_seconds` |
| `CollageBurst` | A burst of image/video cards over a background clip | `backgroundSrc`, `curtainStartSeconds`, `curtainEndSeconds`, `clips`, `duration_seconds` | `duration_seconds` |
| `LyricOverlay` | Lyrics over a music video | `videoSrc`, `lyrics` ([{text, inSeconds, outSeconds}]), `duration_seconds` | `duration_seconds` |
| `EndTag` / `EndTagOverlay` | A closing line on black, or over footage (`overlay: true`) | `text` | fade in + hold + fade out |
| `CaptionOverlayOnly` | Word captions alone (for compositing) | `words` | last word's end + 0.5 s |
| `HeroTitle` | A single title card | `title` | 5 s, or `duration_seconds` |

Every composition also accepts `width`, `height` (even integers), `fps` (default 30) and `duration_seconds`. Defaults are 1920x1080, except `TalkingHead`, `CollageBurst`, `LyricOverlay` and `ProductRevealVertical`, which are 1080x1920.

`renderer_family` picks the composition for templated work: `explainer-data`, `explainer-teacher`, `product-reveal`, `screen-demo` and `animation-first` use `Explainer`; `cinematic-trailer` and `documentary-montage` use `CinematicRenderer`; `presenter` uses `TalkingHead`.

## Explainer props

| Prop | Meaning |
|---|---|
| `cuts` | The scene timeline (required, non-empty). Cuts are in time order and do not overlap. |
| `overlays` | Optional elements over the cuts: `section_title`, `stat_reveal`, `hero_title` (need `text`), `provider_chip` (needs `providers`), each with `in_seconds`, `out_seconds`; optional `subtitle`, `accentColor`, `position`, `cycleSeconds`, `label`. |
| `captions` | Word-highlight captions: `{words: [{word, startMs, endMs, pageBreakAfter?}], wordsPerPage?, highlightColor?, position?: bottom|center|top, fontSize?}`. Default 4 words a page in portrait, 6 in landscape. A page ends at `pageBreakAfter` (from a narration timing record: every sentence and every narration line) and a long sentence splits into even pages. A page leaves 0.7 s after its last word, so pauses are clean. Charts, KPI grids, progress bars and terminal scenes are laid out clear of the caption band; centred cards keep the full canvas, and media cuts stay full-frame under the captions. |
| `audio` | `narration: {src, volume?}`; `music: {src, volume?, offsetSeconds?, loop?, fadeInSeconds?, fadeOutSeconds?, duck?: {ranges: [[start, end]...], level?, attackSeconds?, releaseSeconds?}}`; `sfx: [{src, atSeconds, volume?}]`. Volumes are 0–1. Music fades in 2 s and out 3 s by default; while narration speaks (the `duck` ranges) it plays at `level` (default 0.25) of its volume, ramping over 0.15 s down and 0.4 s up. |
| `theme` | A style id: `clean-professional`, `flat-motion-graphics` (default), `minimalist-diagram`, `premium-minimalist`, `anime-ghibli`. |
| `themeConfig` | Overrides of theme fields: `primaryColor`, `accentColor`, `backgroundColor`, `surfaceColor`, `textColor`, `mutedTextColor`, `headingFont`, `bodyFont`, `monoFont`, `chartColors`, `springConfig`, `transitionDuration`, `captionHighlightColor`, `captionBackgroundColor`. |

An explicit `duration_seconds` times `fps` must be a whole number of frames, and every cut must end by it. Audio longer than the video is cut off at the end.

## Cut fields every cut can use

`id` (unique; recommended), `type`, `in_seconds`, `out_seconds` (required, `0 <= in < out`, at least one frame), `transition` and `transition_duration` (see below), `backgroundColor`, `color`, `accentColor`, `fontSize`, `backgroundImage` or `backgroundVideo` behind a card (with `backgroundVideoStart`, `backgroundOverlay` 0–1 darkening).

## Cut types

| `type` | Required | Optional |
|---|---|---|
| media cut: no `type`, or `image` / `video` | `source` | `media_kind` (image or video, when the extension doesn't say), `animation` (images), `source_in_seconds` (video start within the source) |
| `text_card` | `text` | `fontSize`, `color` |
| `hero_title` | `text` | `heroSubtitle` |
| `section_title` | `text` | `subtitle`, `position` (top-left, bottom-left, center) |
| `callout` | `text` | `callout_type` (info, warning, tip, quote), `title` |
| `stat_card` | `stat` | `subtitle` |
| `stat_reveal` | `text` | `subtitle`, `position` (center, bottom-right, right) |
| `comparison` | `leftLabel`, `rightLabel`, `leftValue`, `rightValue` | `title`, `cardBackgroundColor` |
| `bar_chart` | `chartData`: [{label, value}] | `title`, `chartColors`, `chartAnimation`, `showGrid`, `showValues` |
| `line_chart` | `chartSeries`: [{label, data: [{x, y}], color?}] | `title`, `xLabel`, `yLabel`, `showGrid`, `showMarkers`, `showLegend`, `chartAnimation` |
| `pie_chart` | `chartData`: [{label, value}] | `title`, `donut`, `centerLabel`, `centerValue`, `showLegend` |
| `kpi_grid` | `chartData`: [{label, value, prefix?, suffix?, change?, icon?}] | `title`, `columns` (2–4), `chartAnimation` (count-up, pop, cascade) |
| `progress_bar` | `progress` (0–100) | `title`, `progressLabel`, `progressColor`, `progressSegments` [{value, color?, label?}] |
| `terminal_scene` | `steps`: `{kind: "cmd", text, typeSpeed?, holdSeconds?}`, `{kind: "out", text, holdSeconds?}`, `{kind: "pause", seconds}`, `{kind: "pill", text, color?, durationSeconds?}` | `terminalTitle`, `prompt` |
| `screenshot_scene` | `backgroundImage`, `screenshotSteps`: `cursor_move` {to: [x, y]}, `click_pulse` {at?}, `type_into` {region, text}, `bubble_append` {region, text, role?, stream?}, `typing_dots`, `highlight_box`, `callout_balloon`, `pause` {seconds}; points and regions are 0–1 of the screenshot | `screenshotSize` {width, height}, `cursorStartAt` [x, y] |
| `anime_scene` | `images` (one or more) | `animation`, `particles`, `particleColor`, `particleCount`, `particleIntensity`, `vignette`, `lightingFrom`, `lightingTo` |
| `provider_chip` | `providers` (names) | `cycleSeconds`, `label`, `position` (corners) |

`terminal_scene` and `screenshot_scene` are synthetic: label them as such when they stand in for a real product.

**Camera moves** (`animation` on image and anime cuts): `zoom-in` (default), `zoom-out`, `pan-left`, `pan-right`, `ken-burns`, `ken-burns-slow-zoom`, `parallax`, `static`, `none`, plus `drift-up` and `drift-down` for `anime_scene`.

## Transitions

`transition` on a cut is the move into the next cut: `cut` (default), `fade`, `slide`, `wipe`, `flip`, `clock-wipe`, `iris`. `transition_duration` is in seconds (default from the theme, 0.3–1.0 s).

The next cut still starts at its own `in_seconds` and animates in over that duration; the outgoing cut keeps playing beneath it for the same time. Cut times therefore stay exactly as authored, and a fade is a true crossfade. A transition on the last cut has no effect.

## Layout at any size

Cards, charts and titles are laid out on a 1920x1080 canvas for landscape output and a 1080x1920 canvas for portrait output, scaled to the real frame and centred. Media cuts, `screenshot_scene` and `anime_scene` fill the real frame directly. Check portrait frames with `frame_sample` before delivery: dense landscape charts may need fewer bars or shorter labels in portrait.

## Fonts

Inter, Space Grotesk, Playfair Display, Noto Sans and JetBrains Mono are bundled and loaded before the first frame. Rendering never fetches fonts from the network.
