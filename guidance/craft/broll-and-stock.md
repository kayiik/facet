# B-roll and stock

B-roll is the footage and imagery that shows what the narration talks about: places, people, objects, processes. It sets context, proves the words are real and keeps the picture changing. Plan it from the `script` in `scene_plan`, source it in `assets`, and cut it in `edit`.

## Stock, generated, or rendered

| Need | Use |
|---|---|
| A real place, office, city or landscape | stock; generate only when nothing fits |
| Realistic people | stock; generated people often look uncanny |
| Ambient motion: waves, traffic, clouds, crowds | stock video |
| Historical, archival, scientific, a named place or public figure | `wikimedia` |
| Specific real equipment or products | stock or supplied photos; real is credible |
| An abstract concept or metaphor | generated image or video, or a composition scene |
| A stylised or branded image in the video's look | generated, prompted with the style |
| Diagrams, numbers, names, any exact text | composition scene types, never stock or generated pictures |

Rule of thumb: if it must look real, use stock; if it must be specific to your concept, generate it; if it must be exact, render it.

## From script to shot list

Walk the script section by section: what is the narrator talking about, what visual cue did the writer leave, is it concrete (stock) or abstract (generated or a diagram), and how long do its narration lines run. Write one entry per slot:

```text
slot s3-broll, lines l7–l8 (15.2–21.0 s)
need: establishing shot of a modern data centre
source: stock video; fallback: generated still of server racks with a slow push-in
queries: "data center aisle", "server racks blue light", "server room dolly"
length: 4–6 s; orientation: landscape
subject: rows of server racks; motion: status lights blinking; scene: interior, cool light;
framing: wide, centred aisle, deep; camera: slow push forward, steady
```

## Write queries that find things

- Two to four keywords, subject first: "ocean waves", not "beautiful calm serene ocean waves at dawn".
- Specific, not over-specific: "aerial city skyline sunset" works; a street name and a time of day finds nothing.
- Add the point of view, which libraries index: drone, aerial, over-the-shoulder, macro, top-down, dashcam, handheld, locked-off.
- Add the quality you need: close-up, timelapse, slow motion.
- The same query returns the same results. When it fails, change the words (synonyms, broader terms, another point of view); don't repeat it.

| Shot | Pattern | Example |
|---|---|---|
| Establishing | place, time of day, point of view | "tokyo skyline night drone" |
| Activity | person, action, point of view | "scientist microscope over shoulder" |
| Object | object, style, point of view | "circuit board macro top-down" |
| Nature | element, quality, point of view | "ocean waves aerial" |
| Abstract motion | movement, style | "light trails timelapse" |
| Workplace | setting, activity, point of view | "office meeting handheld" |

## Which tool

Check `capabilities` for `video_stock` and `image_stock` first.

- **`pexels_video`:** curated, sharp real-world footage. `orientation` (`portrait` for vertical), `size: "large"` for 4K, `min_duration` and `max_duration` to skip clips that are too short or needlessly long. Needs a Pexels key.
- **`pixabay_video`:** a large library with `category` filters; `video_type: "animation"` for animated clips, `"film"` for live action. Needs a Pixabay key.
- **`wikimedia`:** Wikimedia Commons, no key: archival, historical and scientific material, places, public figures, maps. `kind: "image"` for photos and illustrations. Quality varies; check every result.
- **`direct_clip_search`:** many slots in one call. Each entry in `queries` has a `query` and a `slot_id`; it tries Pexels and Pixabay when their keys are set, then Wikimedia, up to `clips_per_query`, and writes thumbnails for a quick look. Use it for montage and documentary work with many slots.
- **`video_selector`, `image_selector`:** which video and image providers are configured for a shot, with their limits, when you are choosing between stock and generation.

With no stock keys, `wikimedia` still works, and composition scenes need no assets at all.

## Judge every result before using it

Look at it: the thumbnails, or `frame_sample` frames, and `media_probe` for size, length and frame rate.

- **Relevance:** it shows what the slot needs, not merely something matching the keyword.
- **Point of view:** a wrong point of view (handheld where you need a drone shot) costs more than wrong colour. Re-query rather than crop.
- **Resolution:** at least the output size. For vertical, take `portrait` clips, or 4K landscape when you must crop 9:16 from the centre; a 9:16 crop of a 1080p landscape clip is too soft.
- **Length:** longer than the slot, so the cut has handles; you can trim, not extend.
- **Motion:** smooth, no jarring moves unless wanted, no cut inside the clip. On long clips, `scene_detect` finds the clean shots; start the cut at the best moment with `source_in_seconds` or trim with `video_trimmer`.
- **Look:** light and colour close to the style; no watermarks, logos or burned-in text.
- **Sound:** stock audio is dropped; don't choose a clip for it.

The verdict is one of: use as is; use with a trim, crop or grade; search again; wrong. Use only the first two.

## When search fails

1. Change the words: synonyms, broader terms, another point of view.
2. Try another provider.
3. Generate it from the five-aspect description (`guidance/craft/image-generation.md`, `guidance/craft/video-generation-prompting.md`). Paid generators use the operator's credits; the free alternative is a `wikimedia` still with a camera move, or a composition scene.
4. Ask the operator, showing the best candidates and the alternative.

## Cutting b-roll

- B-roll shows what the narration says at that moment: tie the cut to the line that names it (`lines` in `edit_decisions`).
- Hold b-roll 3 to 6 s in explainers, 2 to 4 s in montage and short-form.
- Vary shot sizes across consecutive clips: wide, medium, close. In montage, carry the direction of movement across cuts and cut on motion.
- Every still gets a camera move (`animation`); alternate directions.
- Avoid stock clichés (handshakes, hands on a keyboard, a glowing globe) unless the script needs exactly that. Specific and concrete beats generic.
- Grade clips from different sources together with `color_grade`, one profile and intensity for the whole video, so they read as one film (`guidance/craft/color-grading.md`).
- Record where each clip came from in the `asset_manifest`: provider, id, query, page address, local path and slot.

## Quality checks

- Every b-roll shot matches the words spoken over it.
- No watermarks, wrong points of view, cuts inside clips, soft upscales or wrong orientation.
- Each clip covers its slot with handles; no frozen last frames.
- Colour is consistent across sources.
- Exact text and numbers come from composition scenes, not from footage or generated images.
- Every clip's source is recorded.
