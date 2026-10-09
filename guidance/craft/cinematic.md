# Cinematic

"Cinematic" describes nothing until it becomes choices: frame shape, lens, light, grade, shot length, sound layers and silence. A cinematic piece builds feeling through images and rhythm, with sparse words, toward a reveal and a landing. It is led by the `cinematic` stance and rendered by `CinematicRenderer` (renderer families `cinematic-trailer` and `documentary-montage`), which takes `scenes`; `HeroTitle` and `TitledVideo` cover title-led pieces. The story shape (tension, reveal, landing) is in `guidance/craft/storytelling.md`.

## Turn mood words into choices

| Word in the brief | What to plan |
|---|---|
| epic | 2.39:1, wide lenses, low angles, shots of 8 s and more, a score that builds to a crescendo |
| moody | low-key light, deep shadows, haze; `moody_dark` at 0.6; shots of 6 s and more; an ambient bed |
| intimate | 40 to 50 mm, shallow focus, close framing; narration or dialogue with room tone; no music under the words |
| tense | tighter framing, shorter shots building toward the turn, a low pulse or drone, then sudden quiet |
| wonder | slow pushes and reveals of scale, light breaking through, the score opening up on the reveal |

Never pass a mood word on to the `scene_plan` or a prompt; pass the choices.

## Frame shape

| Ratio | Picture inside 1920×1080 | Bars top and bottom | Feel |
|---|---|---|---|
| 2.39:1 | 1920×804 | 138 px | grand, anamorphic |
| 2.35:1 | 1920×816 | 132 px | classic scope |
| 1.85:1 | 1920×1038 | 21 px | a subtle widescreen |
| 16:9 | 1920×1080 | none | the default |

- Letterbox only when the piece benefits. Never on screen recordings, talking heads or text-heavy explainers, and never because the pipeline is called cinematic.
- In Facet: render the composition at the picture size (`width` 1920, `height` 804), then `source_edit` with a 1920×1080 `target` and `fit: "contain"` adds the black bars. From a finished 16:9 render, crop first (`fit: "cover"` to 1920×804), then pad.
- Frame every shot for the band that stays visible; titles and captions sit inside the picture, clear of its edges.

## Frame rate

24 fps when the material is generated or animated and the clips are 24 fps (`fps` in `edit_decisions`). Otherwise match the dominant source (`media_probe`); converting 30 fps footage to 24 judders.

## Shots and rhythm

| Style | Average shot | Cuts a minute |
|---|---|---|
| Action, intense | 2–4 s | 15–30 |
| Standard cinematic | 4–8 s | 8–15 |
| Documentary | 6–12 s | 5–10 |
| Contemplative | 10–20 s | 3–6 |
| Montage on music | 1–3 s | 20–40 |

- Breathe: vary length on purpose (8, 5, 3, 2, 10, 6 s); never the same length three times in a row.
- The reveal and the landing hold longest. Give the reveal room: no cut, no words, no music for a beat.
- Cut on emotion first. In order (Murch): emotion, story, rhythm, eye trace, screen direction (keep the 180-degree line), space. Between generated shots, rhythm and story matter more than spatial continuity, but keep the direction of movement across cuts.
- Cuts land on musical turns: a downbeat, a phrase end, the drop.
- Transitions are cuts, with fades for time passing and a fade through black before the landing at most.

## Pictures

- **Motion is the promise.** Motion beats are real clips: supplied footage, stock or generated video. Never swap them for stills with camera moves without asking first.
- **Source footage leads;** generated shots fill gaps. Documentary montage uses real footage only unless the operator asks otherwise (`direct_clip_search` for many slots, `wikimedia` for archive), and credits every clip.
- **Hero frames** (the opening, the reveal, the final image) are specified in full with the five-aspect description, light and grade, and sampled before anything else (`guidance/craft/video-generation-prompting.md`).
- **Lens and light carry the look:** wide lenses for scale, long lenses to isolate, shallow focus for intimacy; motivated light (a window, a lamp, the sun low behind the subject); haze to give light a shape.
- **Prompts** share one style block: lens, light, grade, grain and three to five anchor colours.

## Sound in four layers

| Layer | Level | Content |
|---|---|---|
| Narration, dialogue | peaks −12 to −6 dB | sparse; lines sit between images |
| Score | 18 to 20 dB below the voice under speech; up to 6 to 12 dB louder without it | 60 to 90 BPM, orchestral, piano, ambient or cinematic electronic; dynamic, not loop-based |
| Ambience | −30 to −24 dB | the place: wind, city, room tone |
| Foley, effects | −18 to −12 dB | specific actions, impacts on reveals |

- Silence is the strongest cue: drop the music for 3 to 5 s at the key reveal, then bring it back.
- Place key changes and swells on the story's turns.
- Keep the dynamics: quiet passages stay quiet while the whole mix still meets −14 LUFS integrated.
- `capabilities` reports `sfx` as unavailable: effects and ambience come from files the operator supplies, as `sfx` cues in `audio_mix`. Veo and Sora generate ambience and foley with their clips; keep it when it fits.
- Narration runs slow (120 to 140 words a minute) with long pauses; images carry the rest.

## Grade

One look for every clip, applied with restraint (`guidance/craft/color-grading.md`): `cinematic_warm` at 0.85 for human warmth, `moody_dark` at 0.6 for drama, `vintage_film` at 0.7 for memory, a custom teal-and-orange chain for a blockbuster look. Shadows slightly lifted, never pure black; highlights rolled off, never pure white; skin natural. Grade the clips, never the titles.

## Titles and the end

- Title cards hold 3 to 6 s, in a serif or a condensed sans, centred or low in the frame, with fade or mask reveals (`guidance/craft/typography.md`).
- A documentary montage closes on music and an end tag (`EndTag`).

## Quality checks

- The brief's mood words have become stated choices of shape, lens, light, grade, length and sound.
- The arc is visible: tension builds, the reveal gets room, the landing holds.
- Motion beats are real clips; any fallback was agreed in advance.
- Shot lengths vary; never three the same in a row; cuts land on emotion and on the music.
- Letterbox only where it serves; nothing important under the bars.
- One grade across all sources; faces and text untouched by it.
- The mix keeps its quiet moments, silence at the reveal, intelligible words and controlled swells.
- Every clip's source is recorded; generated and real footage are distinguishable in the `asset_manifest`.
