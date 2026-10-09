# Taste direction

Taste is the set of choices that makes this video look and feel like it belongs to its subject, not to a template. Write it down as a taste profile in the `proposal_packet`, then hold every later stage to it. It is not a style recipe: a preset style is one possible answer, never the starting point.

## 1. Make a design read

One sentence on what the video must feel like and why, tied to the audience, the promise, the platform and the subject.

Good reads are specific:

- "Investor-facing AI launch: precise, restrained, credible; no hype visuals."
- "Youth science short: bright, curious, kinetic; make invisible physics feel touchable."
- "Security incident explainer: tense and surgical; high contrast, low ornament, readable evidence."

Weak reads are only adjectives: "modern and clean", "cinematic", "professional". The test: could this read describe a video on any topic? Then it is too generic.

## 2. Set three dials

Each from 1 to 10, set before writing concepts:

| Dial | Low (1–3) | Middle (4–6) | High (7–10) |
|---|---|---|---|
| Visual variance | one tight system, repeated grammar | a few scene families, changed on purpose | each beat may have its own visual mode |
| Motion intensity | calm holds, small transitions | clear motion accents and reveals | fast kinetic language, frequent changes of direction |
| Information density | one idea a frame | a main idea with supporting detail | dashboards, diagrams, layered callouts |

The dials explain later choices:

- High motion with low density means short kinetic beats, not dense diagrams.
- Low motion with high density means stable frames, chart builds and long, readable holds.
- High variance needs stronger anchors so the video stays one piece: recurring type, palette, framing or a sound motif.

Starting points by stance, to adjust to the read:

| Stance | Variance | Motion | Density |
|---|---|---|---|
| `explainer` | 4 | 4 | 5 |
| `product` | 6 | 7 | 3 |
| `cinematic` | 5 | 4 | 2 |
| `editorial` | 3 | 3 | 4 |
| `animation` | 7 | 6 | 3 |

## 3. Choose the style path

| Path | Use when |
|---|---|
| A Facet style | one of `clean-professional`, `flat-motion-graphics`, `minimalist-diagram`, `premium-minimalist`, `anime-ghibli` honestly matches the read |
| Atelier art direction | the subject needs its own visual world, or the piece is hero work: palette, type, motion character, layout system and one signature device, written before any composition code |

Never let what is available override the read. If no style fits, say so and propose atelier work (`guidance/runtimes/composition-modes.md`) rather than forcing the nearest preset. When a style fits, use it as written.

Palette discipline: a neutral base and one accent is a sound default. Take colour from the subject (the product's own colours, the material of the place, the data's meaning), not from habit.

## 4. Plan references

When the look depends on generated images or video, a mood board, brand assets or atelier work:

- One reference still per scene family or major beat, made or chosen before any batch. Don't squeeze the whole direction into one mood-board image.
- Brand and product work: inspect the real brand first (logo, colours, type, the product's interface) and use it.
- Screen demos: look at the real interface and note what to emphasise before styling overlays.
- Every reference maps to a scene family. A board that looks good but maps to nothing is decoration.

## 5. Carry it downstream

- **`proposal`:** the taste profile goes in the `proposal_packet` with the design read, the three dials, palette discipline, layout variation, reference strategy, anti-patterns and quality gates. Explain how the dials shape the runtime, the composition mode and the asset plan.
- **`scene_plan`:** variance sets how much layouts change; density caps on-screen text and callouts; motion sets transitions and camera moves. Low variance means one consistent system, not one scene type: `plan_check` still wants variety.
- **`assets`:** prompts carry the palette, texture, framing and lighting of the read; reference stills come before full batches.
- **`edit` and `compose`:** hold lengths and cut rhythm follow the motion dial; no decorative overlay that contradicts the read.

How the dials map to Facet choices:

| Dial | Low | High |
|---|---|---|
| Motion | `cut` and `fade`; `static` or slow `ken-burns` on stills; long holds | `slide`, `wipe` and the occasional `iris`; `zoom-in`, `pan-left`, `parallax`; short holds; kinetic type |
| Density | `hero_title`, `text_card`, `stat_card`, full-frame pictures | `kpi_grid`, multi-series `line_chart`, `terminal_scene`, `callout` with detail, longer holds |
| Variance | one layout system, one transition family, recurring framing | different scene families per beat, anchored by type, palette or sound motif |

An example profile:

```json
{
  "design_read": "Premium expert explainer: calm authority, high trust, low ornament.",
  "visual_variance": 4,
  "motion_intensity": 3,
  "information_density": 5,
  "palette_discipline": "Neutral base, one accent, no decorative gradients.",
  "layout_variation": "Alternate editorial split frames with data-forward full-frame scenes.",
  "reference_strategy": "One reference still per scene family before generating assets.",
  "anti_patterns": ["generic purple gradient backgrounds"],
  "quality_gates": ["Every scene carries the design read without explanatory labels."]
}
```

Record it with the field names the `proposal_packet` schema uses.

## Anti-defaults

Flag these before moving on:

- Purple AI gradients or default corporate blue with no reason in the subject.
- The same transition on every cut when visual variance is 4 or more.
- Kinetic motion that makes the narration harder to follow.
- Dense callouts when information density is 4 or less.
- Text-only slides, unless typographic storytelling is the read.
- A mood board that does not map to scene families.
- A brand or product video that never shows the real brand or product.

## Quality checks

- The design read names a real creative choice, tied to audience and subject.
- The scene plan and the cut respect the three dials.
- The anti-patterns are absent from the rendered frames (check a `contact_sheet` from `output_review`).
- A reference strategy exists wherever generated images, video or atelier work depend on visual nuance.
- Swap the title for another topic: if the video would still fit, the taste direction is too generic.
