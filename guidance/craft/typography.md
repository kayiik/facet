# Typography

Text in video is read once, in motion, often on a phone. It must be big, short, high-contrast, inside the safe area and on screen long enough to read. The style (`theme` in `edit_decisions`) sets the type pair and colours; change them only with a reason. Caption rules are in `guidance/craft/captions.md`.

## Fonts

- One or two families a video. More is noise.
- Pair a bold display face for titles with a neutral face for body text; titles at least 50% larger than body.
- Sans-serif for motion graphics, labels and captions; it holds up while moving. Serif for cinematic title cards and editorial work. Script or decorative faces only for a hero title, never for body text, never in motion.

| Use | Faces |
|---|---|
| Body, captions | Inter, Open Sans, Roboto, Source Sans, Lato, DM Sans |
| Headlines, stats | Montserrat Bold, Bebas Neue, Oswald Bold, Poppins Bold |
| Editorial, cinematic | Playfair Display, Roboto Slab |
| Fallback | Helvetica Neue, Arial, Avenir Next |

| Heading | Body | Feel |
|---|---|---|
| Bebas Neue | Open Sans | high-impact social |
| Montserrat Bold | Lato | clean, modern |
| Playfair Display | Inter | editorial |
| Poppins Bold | Poppins Light | one family, clear hierarchy |

Burned captions use the font you give `ffmpeg_caption_burn` as `font_file`; without it they fall back to a system face. Atelier compositions load their own fonts; check the first rendered frames.

## Sizes

| Element | 1920×1080 | 1080×1920 vertical |
|---|---|---|
| Title, hero text | 60–90 px | 72–110 px |
| Body, labels | 40–60 px | 48–64 px |
| Captions | 42 px and up (3–5% of the height) | 56–80 px, bold |
| Lower third: name / role | 48–60 / 36–44 px | 52–64 / 40–48 px |

Running text is never under 40 px at 1080p; chart labels have their own floor (24 px for axes and values, 16 px for source lines) in `guidance/craft/data-visualization.md`. `ffmpeg_caption_burn` takes `font_size` in pixels at the video's own height, so 4% of 1080 is about 43 px and 4% of 1920 about 77 px. A title must still read on a thumbnail.

## Safe areas

- **Horizontal:** keep all text inside the title-safe area, the central 80% (192 px in from the sides and 108 px from top and bottom at 1920×1080); keep every important element inside the central 90%.
- **Vertical:** keep text inside the central 900×1400 px of a 1080×1920 frame. The bottom 300 to 320 px is covered by platform buttons and descriptions, the top 100 to 200 px by the header. Per-platform margins are in `guidance/craft/short-form.md`.
- Captions sit at least 60 px above the bottom edge in horizontal, above the bottom 320 px in vertical, and within 90% of the frame width.

## Line length and amount

- Captions: at most 42 characters a line, two lines. `subtitle_gen` cues 6 to 8 words in horizontal and 3 to 4 in vertical, which keeps vertical lines near 20 to 30 characters.
- Overlays: at most 30 characters a line and three lines. Vertical on-screen text: 3 to 5 words a block, in the upper 40% of the safe area, away from the captions.
- One focal element a frame. Hierarchy comes from size first, then weight, then colour; one accent colour, used for emphasis only.
- Exact text (names, numbers, prices, calls to action, legal lines) is rendered by the composition as text, never painted into a generated image, and spelled exactly as in the `brief` or `research_brief`.

## Time on screen

- Hold any text for at least one second per 13 characters after its entrance finishes: a 30-character line needs about 2.3 s. Three seconds covers about 63 characters.
- Title cards: 3 to 6 s. Lower thirds: 3 to 6 s on screen.
- Captions: at least 1 s, at most 6 to 7 s, read at up to 21 characters a second, with a gap of about 2 frames between cues.
- Text that changes faster than it can be read twice is decoration. Cut words before you cut time.

## Entrances and exits

| Move | Duration | Use |
|---|---|---|
| Fade | 0.3–0.5 s | subtle, any style |
| Slide or scale | 0.5–1.0 s | standard motion graphics |
| Scale pop | 0.2–0.3 s | short-form text blocks |
| Kinetic entrance, character stagger | 1.0–2.0 s | bold titles, kinetic type |
| Lower third in / out | 1–2 s / 0.5–1 s | speaker names |

- Ease out on entrances (the text decelerates into place), ease in on exits, ease in and out for moves. Never linear; it looks mechanical.
- Useful curves: ease-out cubic `(0.33, 1, 0.68, 1)` as the default entrance; ease-out quart `(0.25, 1, 0.5, 1)` for snappier kinetic type; ease-in-out cubic `(0.65, 0, 0.35, 1)` for scale and opacity; ease-in cubic `(0.32, 0, 0.67, 0)` for exits.
- Reveals by feel: a mask reveal reads premium; a scale pop reads energetic; word-by-word reads conversational; a fade reads corporate.

## Contrast

- Body text at least 4.5:1 against what is behind it; large text at least 3:1; aim for 7:1. White on black is 21:1.
- Over footage or images, in order of reliability: a semi-transparent box (black at 70 to 80%), a 2 to 4 px dark stroke, a gradient scrim behind the text area, a 30 to 50% dark overlay across a text-heavy frame. A drop shadow alone fails on busy backgrounds.
- Check contrast on graded frames, not the design file; grading changes it.

## Lower thirds

The name in a bold weight, the role lighter and smaller, on a bar or with a shadow, inside the title-safe area. When captions are at the bottom, place the lower third above them or move the captions (`position: "top"`) while it shows.

## Quality checks

- No running text under 40 px at 1080p (chart labels excepted); captions 42 px or more in horizontal, larger in vertical.
- All text inside the safe area for the delivery format; nothing under platform buttons.
- Contrast meets 4.5:1 on the final graded frames.
- At most two families; titles clearly larger than body.
- Every text holds for one second per 13 characters; no caption over two lines or 42 characters a line.
- Names, numbers and calls to action are spelled exactly as sourced.
- Captions never collide with on-screen text. Check with `frame_sample` or the `output_review` `contact_sheet`, and view a few frames at phone size.
