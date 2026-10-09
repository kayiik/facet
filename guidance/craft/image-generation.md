# Image generation

Generated stills give you images that must be specific to the concept or drawn in the video's own look: a metaphor, a stylised scene, a hero frame, the first frame of a generated clip. Every call is paid. The art director sets the look in the `proposal_packet` and the `scene_plan`; images are made in `assets`.

## Before the first call

1. **Choose the source.** If the image must look real, use stock: `wikimedia` with `kind: "image"` for photos, places, history and science, or a frame of stock footage. Names, numbers, labels and diagrams are composer scenes (`text_card`, `callout`, `stat_card`, the chart types), never painted into a picture. Generate only when the image must be specific to the concept or match a stylised look.
2. **Check availability.** `capabilities` reports `image_generation`. When it is unavailable, offer the free path; when it is available, still put the free path beside the paid one.
3. **Announce each call** before making it: tool, provider, model, why, sample or batch, and the tool's credit note from `describe` ("uses your … credits").
4. **Hero first.** Generate the most important image, get it approved, then make the rest from it.
5. **Ask before switching** tool or model partway through a set.

## Which tool

| Tool | Shapes | Reach for it when | Controls |
|---|---|---|---|
| `flux_image` | 1:1, 16:9, 9:16, 4:3, 3:4, 21:9 | photoreal and illustrated scenes, hero images | `seed` (returned with each image; reuse it for variations), `guidance_scale` (higher follows the prompt more literally) |
| `openai_image` | 1:1, 16:9, 9:16, 4:3, 3:4 | complex instructions with several elements | `quality` `standard` or `hd`; `style` `natural` (photographic) or `vivid` (dramatic, saturated) |
| `gflow_image` | `landscape`, `portrait`, `square`, 4:3, 3:4 | several options in one call; carrying a subject or look forward | `count` up to 4; `reference_image`; `model` |

FLUX prompting in depth: `guidance/vendor/flux-best-practices/SKILL.md`.

## Size and shape

- Generate in the frame's shape: 16:9 for landscape video, 9:16 for vertical. Cropping a landscape image to vertical throws away two thirds of it.
- Check each image's pixel size with `media_probe`. An image smaller than the frame is scaled up and goes soft, and a camera move magnifies it further. Give small images gentle moves (a slow `zoom-in`, or `static`), or use them smaller in the frame.
- Leave room for text. When a title or label will sit on the image, ask for empty space where it goes: "subject on the right third, open sky on the left".
- Vertical: keep the subject in the centre, clear of the bottom third, where captions and platform buttons sit (`guidance/craft/short-form.md`).

## Write the prompt in three layers

1. **Shot:** size, angle, light, depth of field, texture.
2. **Style anchor:** 5 to 10 words that carry the video's look: medium, palette, light, texture. Use the same anchor in every image of the video; a long style paragraph pasted everywhere makes every image look alike.
3. **Subject:** the concrete subject, what it is doing, where.

| Weak | Strong |
|---|---|
| a person using a computer in a modern office | a developer in a dim home office, blue monitor glow on his glasses, desk cluttered with sticky notes and an empty mug |

```text
Medium close-up, golden-hour light from the left, shallow depth of field.
Muted earth tones, soft shadows, fine film grain.
A beekeeper in white protective gear lifts a frame dripping with honey;
late sun catches the drops; a lavender field blurs behind her.
```

- Two or three sentences. FLUX does best at 30 to 80 words, in prose rather than keyword lists.
- Front-load the subject: models weigh what comes first.
- Always name the light; it does more for quality than anything else.
- Name 3 to 5 palette colours. For brand colours, give the hex code with a name: "#0066CC (company blue)".
- Say what you want, not what you don't. FLUX has no negative prompt, and naming an unwanted thing can bring it in: "clean unmarked surfaces", not "no text"; "a deserted street", not "no people".
- Camera bodies, lenses and apertures ("85 mm, f/1.8") help only photoreal images.

Starting anchors for each style; adapt them to the scene:

| Style | Anchor |
|---|---|
| `clean-professional` | clean flat illustration, light background, restrained corporate palette, soft shadows |
| `flat-motion-graphics` | flat vector illustration, bold colours, geometric shapes, dark background |
| `minimalist-diagram` | minimalist line drawing, clean lines, light background, blueprint feel |
| `premium-minimalist` | editorial frame, restrained palette, precise composition, generous negative space |
| `anime-ghibli` | hand-painted anime look, watercolour textures, soft diffused light, lush nature, warm palette |

Other looks: isometric ("isometric 3D illustration, 30-degree view, clean geometric shapes, soft shadows"), watercolour ("soft watercolour, visible brush strokes, paper texture, muted tones"), photoreal ("photograph, 50 mm, natural window light, shallow depth of field").

## Keep a set consistent

The hard part is making eight to twelve images look like one video.

1. **One anchor** in every prompt, as above.
2. **A hero reference.** Approve the hero, then pass it to `gflow_image` as `reference_image` for the rest: "the same woman as in the reference, now at a train window at night".
3. **The seed.** `flux_image` returns the `seed` it used; the same seed with a similar prompt gives a similar composition and light. It breaks when the prompt changes much, so use it with the anchor, not instead of it.
4. **Recurring subjects** get the same 3 to 6 attributes, word for word, in every prompt.
5. **One tool** for the set; each model has its own colour and finish.
6. **No mixing inside a scene:** a scene is all stock or all generated.
7. **`anime_scene`** (the `anime-ghibli` style) crossfades through its `images` under a slow camera move. Make 2 to 3 images a scene with the same composition and palette and one small change each: the light's angle, leaves, a pose.

## Images in the video

- Every still gets a camera move (`animation`: `zoom-in`, `zoom-out`, `pan-left`, `pan-right`, `ken-burns`, `parallax`); alternate directions, and hold 3 to 6 s.
- A still can become a clip: `gflow_video` with `start_frame`, or `kling_video` with `image_url` (`guidance/craft/video-generation-prompting.md`).

## What models do badly

- **Text:** misspelt, warped or invented letters. Put every word in the composer.
- **Hands and fingers:** avoid poses that need detailed hands.
- **Identity:** without a reference, the same character changes every time.
- **Overloaded prompts:** many subjects and instructions produce muddle; one subject, one action, one place.

## Quality checks

- Each image shows what its `scene_plan` entry asked for, in its shot and light.
- No letters, watermarks, extra fingers, melted objects or broken anatomy; look at full size.
- Viewed side by side, the set reads as one video: same medium, palette and light; recurring subjects match.
- Shape matches the delivery and size is at least the frame for full-frame use (`media_probe`).
- Every word and number on screen comes from the composer.
- Each paid call was announced with its credit note; prompt, tool, model and seed are recorded in the `asset_manifest`.
