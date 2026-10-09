# Video generation prompting

Generated video gives you shots no library has: a stylised world, a concept in motion, a product in a place that does not exist. A model renders only what the prompt makes visible, and every call is paid and slow. The director writes each shot in the `scene_plan`; clips are made in `assets` and checked before `edit`.

## Before the first call

1. **Choose the source.** If the shot must look real, use stock (`pexels_video`, `pixabay_video`, `direct_clip_search`); history and named places come from `wikimedia` as a still with a camera move; exact text is a composer scene. Generate only when the shot must be specific to the concept or drawn in the video's own look (`guidance/craft/broll-and-stock.md`).
2. **Check availability.** `capabilities` reports `video_generation`. Unavailable or not, put the free path beside the paid one.
3. **Announce each call:** tool, provider, model, why, sample or batch, and the credit note from `describe` ("uses your … credits"). The operator's yes is the only gate.
4. **Sample first:** the hardest shot once, approved, before the batch.
5. **Ask before switching** tool or model partway, or dropping from motion to stills.

## Which tool

| Tool | Model | Clip length | Shape and size | Reach for it for |
|---|---|---|---|---|
| `gflow_video` | Veo 3.1 | 4, 6, 8 or 10 s | `landscape`, `portrait`, `square`; 720p, 1080p, 4k | photoreal shots, exact camera and lens language; `start_frame` and `end_frame` pin the first and last frames |
| `kling_video` | Kling | 5 or 10 s | 16:9, 9:16, 1:1; `mode` `std` or `pro` | strong subject motion; `image_url` animates an approved still |
| `sora_video` | Sora 2 | up to 20 s | 16:9, 9:16, 1:1; 720p or 1080p | prose-led scenes, film looks, longer takes |

- Generate in the delivery shape (`portrait` or 9:16 for vertical); don't crop later.
- Ask for more length than the slot so the edit has handles (`video_trimmer`, `source_in_seconds`).
- One tool and one model a sequence; models differ in colour, grain and motion.
- A paid call cut off midway may be charged and still running. Ask, then rerun `kling_video` or `sora_video` with its `provider_job_id` as `resume_job_id`.

## Describe the shot in five aspects

Models pick up subject and scene easily and drop motion, layout and camera unless the prompt spells them out. Fill all five:

| Aspect | Write |
|---|---|
| Subject | what it is, with 3 to 6 visual attributes that tell it apart: age, build, clothing, material, colour |
| Subject motion | each action in the order it happens; who does what to what |
| Scene | setting, period, time of day, weather, what moves in the background |
| Spatial | shot size, where the subject sits in frame, what fills foreground, midground and background, camera height, and how any of these change during the clip |
| Camera | playback speed, lens, height, angle, focus, steadiness, movement |

Then one lighting setup (source, direction, colour temperature) and the style (medium, grade, three to five anchor colours).

```text
Shot: medium close-up, slight low angle
Camera: real time; 35 mm; slow dolly in; shallow depth of field, background soft
Subject: a fisherman in his sixties, salt-and-pepper beard, dark wool sweater,
  calloused hands gripping a wet rope
Motion: he pulls the rope hand over hand, pauses, then looks out to sea
Scene: wooden dock at dawn, calm grey water, fog bank on the horizon,
  gulls wheeling far behind him
Lighting: soft overcast; a warm break in the clouds rim-lights his beard
Style: documentary 35 mm film, fine grain; slate blue, weathered grey, amber
Exclude: no text, no subtitles, no watermark
```

- **Self-contained:** someone who never saw the plan should picture the shot from the prompt alone. If a reader could not, the model will not.
- **Order:** events in the order they happen; otherwise by prominence, people before objects, the largest and most central first.
- **Length:** Veo and Sora do best at 100 to 250 words; past 250, more detail rarely helps. Shorter prompts leave the model freedom, longer ones give control: hero shots get the full description, inserts can be shorter.
- **Per model:** Sora reads a prose paragraph best, then a Cinematography block (camera, lens, lighting, mood), then an Actions list of beats. Veo reads a labelled list like the example. For Kling, lead with the subject and its motion: one action, one camera move.
- **Sound:** Veo and Sora generate sound with the picture. Describe it (ambience, footsteps, one speaker's line in quotes) only when the plan keeps the clip's own sound; narration and music come from the voice tools and `audio_mix`. Veo may subtitle speech by itself: write "no subtitles".

## Camera words the models take literally

- **The camera travels:** dolly in or out, truck left or right, pedestal up or down.
- **It pivots in place:** pan, tilt, roll. Dolly is not zoom and pan is not truck; the model follows the word that leads.
- **Only the lens changes:** zoom; rack focus (a snap between two subjects); pull focus (slower); focus tracking (follows a moving subject). Name the start and end focal plane.
- **Signature moves:** arc or orbit, crane, tracking, whip pan, handheld, dolly zoom (save it for a revelation).
- "Static" means no movement, zoom or focus change; if anything moves, name the move.
- Bird's-eye is strictly top-down; aerial is altitude (a drone looking down at 45° is a high angle from aerial height).
- Name the playback speed: time-lapse, fast or slow motion, speed ramp, stop-motion, reversed.

## Light, lens and style

- **One lighting setup:** golden hour, overcast, high-key, low-key, Rembrandt, backlight, rim light, practical lights, volumetric haze. "Bright noon" with "deep shadows" fights itself.
- **Lens:** 24–35 mm for space; 50 mm for a natural view; 85 mm and longer to compress the background and isolate the subject; anamorphic for a wide frame and horizontal flares.
- **Style:** a medium (photoreal, documentary 35 mm, hand-painted 2D, claymation, cel-shaded 3D), a grade (warm Kodak-like, teal and orange, 16 mm black and white), a finish (fine grain, halation, vignette). Name 3 to 5 anchor colours, not "warm tones".

**Causes, not feelings.** "Cinematic", "epic" and "beautiful" do not constrain pixels; write what the camera sees. Not "a sad man" but "tears on his cheek, shoulders slumped, staring at an empty chair"; not "he moves quickly" but "he sprints three steps and vaults the railing".

## Consistency across shots

- Write the style block once (lighting, lens, grade, anchor colours, medium) and paste it word for word into every prompt of the sequence.
- Re-state each recurring subject with the same 3 to 6 attributes, word for word, in every shot. Pronouns and "the same character" do not carry identity.
- Anchor with an image: approve a still (`guidance/craft/image-generation.md`), then animate it with `gflow_video` (`start_frame`) or `kling_video` (`image_url`: an address the provider can fetch, such as the `url` an image tool returns; these may expire, so use it soon).
- Grade the finished clips together with one `color_grade` profile (`guidance/craft/color-grading.md`).

## What models do badly

- Readable text, logos, interfaces and numbers: they go in the composer; exclude text in the prompt.
- Complex physics (explosions, pouring liquid, colliding crowds); walking, turning and drifting hold up.
- Fine hand work; several people talking (one speaker a clip); realistic people, which often look uncanny (take them from stock).
- Overloaded prompts: one scene, one main action, one camera move a clip. A cut needs two clips.

## Iterate

1. Start with subject, motion and setting; add camera, lighting and style one layer at a time.
2. When a shot misfires, strip back: lock the camera, simplify the action.
3. Change one thing per retry; record each prompt and result in the `asset_manifest`.
4. After two failed tries on a shot, stop and offer options: a simpler shot, stock, a still with a camera move, a composer scene.

## Quality checks

- Each clip shows the subject, motion and camera move its `scene_plan` entry asked for. Watch it; pull start, middle and end frames with `frame_sample`.
- No warped hands or faces, melting objects, flicker, morphing, garbled text or watermarks.
- Recurring subjects look the same in every shot; light and colour match across the sequence.
- Size, shape and length fit the delivery (`media_probe`); each clip outlasts its slot.
- Every paid call was announced with its credit note; prompt, tool, model and settings are in the `asset_manifest`.
