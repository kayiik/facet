# Colour grading

Grading makes footage from different cameras, libraries and generators read as one film, and sets the mood the brief asks for. The rule is one look per video, applied with restraint. The editor grades in `edit`, after the cut and before captions; the critic checks it on frames.

## What to grade

- **Footage and generated clips:** stock, supplied recordings, `gflow_video`, `kling_video` and `sora_video` output. `color_grade` works on video files.
- **Not the composition.** Titles, charts, captions, logos and brand colours are drawn exactly by the composer. Grade the clips before they go into the timeline, not the composed render, or every brand colour and caption shifts with the grade.
- **Stills** take their look from the prompt and the style (`guidance/craft/image-generation.md`).
- **A footage-only edit** (`source_edit`, `video_stitch`) is graded once assembled, then captions are burned last.

## The tool

`color_grade` takes `input_path`, `output_path`, a `profile` and an `intensity`. Always name the profile; left out, it applies `cinematic_warm`.

| Profile | What it does | Start for | Intensity |
|---|---|---|---|
| `bright_clean` | slightly brighter, a touch more contrast and saturation | corporate, SaaS, product | 0.8 |
| `neutral` | nothing until you add adjustments | science and education, where true colour matters; corrections | – |
| `cinematic_warm` | warmer shadows and highlights, a little more contrast and saturation | stories, people, documentary | 0.85 |
| `cinematic_cool` | bluer shadows and highlights, slightly less saturation | technology, dark interfaces, night | 0.7 |
| `moody_dark` | lifted blacks, lowered whites, darker, less saturated | drama, serious subjects | 0.6–0.7 |
| `high_contrast` | strong S-curve, contrast +15%, saturation +20% | lifestyle, social, energetic | 0.8; lower with people |
| `vintage_film` | faded blacks, softer whites, warm cast, lower saturation | retro, memory, archive | 0.7 |
| `custom` | like `neutral`; the base for your own adjustments | – | – |

- **`intensity`** blends the graded picture with the original, adjustments included: 0.8 is the default and a good start; 0.5 is barely visible; 1.0 is usually overdone, more so on a phone.
- **Adjustments** stack on top of the profile: `temperature` in kelvin (6500 neutral; lower warms, higher cools; move 300 to 500 at a time), `contrast` and `saturation` (1.0 neutral), `brightness` (0 neutral; steps of 0.02), `gamma` (1.0 neutral; above 1 lifts the midtones). They multiply with the profile's own settings: `high_contrast` plus `saturation: 1.1` is about 1.3.
- **`lut_path`** applies a .cube LUT in place of the profile. Keep LUTs inside the project, use one for the whole video, and blend it at 0.6 to 0.8.
- **`custom_vf`** replaces both with your own FFmpeg filter chain, for looks the profiles do not cover.

## Method

1. **Look at the sources side by side.** `frame_sample` a frame from each clip and note which run warm, cool, dark or flat.
2. **Correct the outliers first.** A clip that is off-white or underexposed gets its own pass: `profile: "neutral"` with `temperature`, `brightness` or `gamma`, at `intensity: 1`. Adjustments given with a LUT apply after it, so correct in a separate pass before a LUT.
3. **Test the look on a slice.** Cut 3 to 5 s of a representative shot with `video_trimmer`, grade it, and compare `frame_sample` frames at the same moment before and after. Include a shot with skin if there is one.
4. **Apply one look to everything.** The same profile on every clip, written to new files. Keep one intensity, except for sources that arrive already graded (stylised generated clips): lower theirs, often to 0.5 or 0.6, until they match their neighbours. Match the look, not the number.
5. **Check the joins.** Frames either side of each cut; no jump in colour or exposure.
6. **Record it.** Profile, intensity, LUT and adjustments go in the `asset_manifest`, so a re-render repeats them.

## A custom chain

Keep this order: white balance, colour balance by shadows, midtones and highlights, tone curve, then contrast and saturation, then a LUT last. Change one value by about 0.05 at a time and compare frames. A teal-and-orange starting point (shadows toward teal, highlights toward orange):

```text
colorbalance=rs=-0.05:bs=0.08:rh=0.08:bh=-0.06,eq=contrast=1.06:saturation=1.05
```

## Skin and detail

- Viewers notice wrong skin first. After grading, look at a frame with a face: orange, green, magenta or grey means pull back.
- With people on screen, keep total saturation at or below about 1.2, and `moody_dark` at 0.6 to 0.7 so skin does not turn grey.
- Keep detail at both ends: shadows not crushed to flat black, skies and faces not clipped to white.

## Mood without words

Warm, saturated light reads as inviting; cool, desaturated light as technical or distant; lifted blacks and low saturation as memory or sorrow; deep contrast as energy or tension. Choose the profile from what the brief asks the viewer to feel, not by habit; `cinematic_warm` is not the default for everything.

## Quality checks

- Frames from every source, side by side, read as one film (`frame_sample` at several points, or the `output_review` `contact_sheet`).
- No colour or exposure jump at any cut.
- Skin looks natural; nothing orange, green or grey.
- No crushed shadows or clipped highlights where detail matters.
- Brand colours, charts, titles and captions are exact: the composition was not graded.
- Text and captions over graded footage still meet 4.5:1 contrast.
- Profile, intensity and any LUT or adjustments are recorded.
