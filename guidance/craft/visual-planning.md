# Visual planning

Visual planning turns the script into what is on screen at every moment, why, how it moves, and whether the configured tools can make it. The director does it in `scene_plan` with the art director; `plan_check` checks it; scene fields are in `guidance/runtimes/scene-types.md`.

## Method

1. **Read each section:** the concept, the writer's visual cue, the beat (as a visual cause, not a mood), and the time it has from its narration lines.
2. **Find the visual idea.** Which metaphor will the audience recognise at once (a network as nodes, encryption as a lock)? How do the best creators show this? What has nobody tried? What can the providers in `capabilities` actually make?
3. **One idea per scene.** Break each section into one to three scenes; a 10 s section usually needs two or three.
4. **For each scene write:** its purpose (the information it carries or the feeling it causes), its render form, its narration lines, its transitions, its overlays listed apart from the picture, and each required asset with its source: supplied, stock, generated or rendered by the composition.

## Techniques worth naming

- **Diagram reveal:** start empty and add each part when the narrator names it. For systems, processes, architecture.
- **Analogy split:** the abstract idea beside its everyday analogy (points in a vector space beside library shelves sorted by topic).
- **Stat punch:** one number full screen, scale-up entrance, held 4 to 5 s (`stat_card`).
- **Data sequence:** overview, then drill in: `kpi_grid`, then `bar_chart` for the breakdown, `line_chart` for the trend, `pie_chart` for the split, grouped with `section_title`.
- **Before and after:** `comparison` with the two values side by side.
- **Timeline:** left to right, each step appearing as it is named.
- **Zoom and focus:** the whole system wide, then a push in on one part (an `image` cut with `zoom-in`).
- **Code walkthrough:** `terminal_scene` with commands and output in step with the narration.
- **Interface walkthrough:** `screenshot_scene`: a cursor, clicks and highlights scripted over a still screenshot, with no screen capture.

## Render forms and holds

| Need | Form | Hold |
|---|---|---|
| Opening title, end card, big reveal | `hero_title` | 3–5 s |
| Chapter label | `section_title` | 2–3 s |
| Statement, key term, call to action | `text_card` | 3–5 s |
| Quote, tip, warning | `callout` | 4–6 s |
| One big number | `stat_card`; `stat_reveal` for a compact badge | 4–6 s |
| Several metrics | `kpi_grid` | 5–7 s |
| Progress, completion | `progress_bar` | 4–6 s |
| A versus B, before and after | `comparison` | 4–6 s |
| Categories, rankings / trend / parts of a whole | `bar_chart` / `line_chart` / `pie_chart` | 5–7 s |
| Code, commands | `terminal_scene` | 5–10 s |
| Software interface | `screenshot_scene` | 4–8 s |
| Illustrated anime-look moment | `anime_scene` | 3–6 s |
| The model behind a generated clip | `provider_chip` | with the clip |
| Real-world context, examples | `video` cut (stock, generated, supplied) | 3–6 s |
| Illustration, metaphor, photo | `image` cut with a camera move | 3–6 s |

When no image or video provider is available, plan with the composition's own scene types: cards, charts, `terminal_scene`, and `screenshot_scene` from supplied screenshots all render without a provider. Footage-led trailers and montage use `CinematicRenderer`; presenters use `TalkingHead`.

## Five aspects for every filmed, found or generated shot

1. **Subject:** type and key attributes; how to tell several apart.
2. **Subject motion:** actions in order. For a diagram, the order in which parts appear.
3. **Scene:** point of view, setting, time of day, dynamics (wind, traffic, crowd). Overlays are listed separately.
4. **Spatial framing:** shot size, position in frame, depth (foreground, middle, background), camera height against the subject, and how these change.
5. **Camera:** playback speed, lens, height, angle, focus and depth of field, steadiness, movement.

For scenes the composition renders, write "camera: N/A" and describe the layout and which element holds the centre. Say N/A explicitly; silent omission is the most common failure and produces vague prompts.

Overlays (titles, captions, lower thirds, labels, chips, watermarks) are not part of the picture's depth. Never call an overlay "in the foreground"; list it with its content and position.

## Shot language

- **Shot size:** wide to say where we are, medium for what is happening, close-up for detail and feeling. Wide to close orients; close to wide releases or reveals scale.
- **Camera moves carry meaning:** a push-in focuses or marks a realisation; a pull-back reveals context or scale; a pan connects two things; a tilt shows height; a locked-off frame gives stillness and authority; handheld gives immediacy.
- **Replace mood words with choices.** "Epic": wide frames, long holds of 8 s or more, a score that builds. "Moody": low-key light, deep shadows, slow holds of 6 s or more, a quiet ambient bed. "Intimate": a normal lens (40 to 50 mm), shallow focus, room tone, no music under speech. "Cinematic" alone describes nothing.
- **Cut for the right reasons, in order:** emotion, story, rhythm, eye trace, screen direction, spatial continuity. Between generated shots, rhythm and story usually matter more than continuity.
- **Eye trace:** keep the point of interest near where the eye already is across a cut; in vertical, keep it inside the safe area (`guidance/craft/short-form.md`).

## Movement and transitions

- A process or transformation moves on screen. A static picture of a dynamic idea fails.
- Every still gets a camera move (`animation`: `zoom-in`, `zoom-out`, `pan-left`, `pan-right`, `ken-burns`, `parallax`); use `static` only when stillness is the point. Alternate directions across consecutive stills.
- `cut` is the default transition. `fade` marks time passing or a section change; `slide` and `wipe` step through a sequence; `flip`, `clock-wipe` and `iris` are spent once or twice a video, at topic shifts. Keep `transition_duration` short (0.3 to 0.8 s) and use the style's transitions consistently; random transitions feel chaotic.

## Hold the look

- Palette, type and transitions come from the style (`theme`); the taste profile sets how much layouts vary, how dense information gets and how hard things move (`guidance/craft/taste-direction.md`).
- Asset descriptions carry the video's own palette and lighting. "Make it flat-motion-graphics" is not a plan; say what makes this video's look distinct.
- Exact text (titles, numbers, names, calls to action, legal lines) is a text scene rendered by the composition, never text inside a generated image.
- Captions and on-screen text never say the same thing at the same moment. Move captions (`position`) when a scene has its own text in the lower third.

## Coverage, variety, feasibility

`plan_check` reports these; fix every failure and explain any warning you accept.

- The first scene starts at 0, the last ends at the full duration; every section and visual cue is covered; no gap over 1 s unless it is a deliberate beat.
- Never three scenes of the same type in a row; at least three types; dense scenes alternate with breathing room; something changes every 3 to 5 s (1 to 3 in short-form).
- Slideshow risk: repeated layouts, decorative pictures with no purpose, motion without reason, runs of text cards. A `motion_led` promise needs real motion in most shots.
- Every asset has a source that exists now. Where a capability is unavailable, plan the alternative and say what changes.

## Quality checks

- Each scene has a purpose; cutting it would lose something.
- Each scene matches what the narrator says while it is on screen.
- Five aspects are present for every shot, or marked N/A.
- Shot sizes and lengths vary; the rhythm alternates.
- Every scene looks like the same video.
- No exact text inside generated images.
- A `contact_sheet` from `output_review` reads as a sequence, not a deck of slides.
