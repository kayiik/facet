# Stage: assets

**Run by:** the producer, who owns the paid calls, with the sound designer for voice, music and effects.
**Records:** `asset_manifest`; `narration_timing` when there is narration.

Plans become files here. Every asset must exist on disk, match the plan, look like the same video, and carry its provenance.

## Inputs

`scene_plan` (required assets per scene), `script` (narration lines, voice plan, pronunciation), the plan in `proposal_packet` (providers, voice, music, paid steps), `source_media_review` for supplied material.

## Method

1. **Inventory.** One task per required asset in the scene plan, plus narration, music and sound effects. Note the tool, the provider and whether it may charge.
2. **Read before you call.** `describe` gives each tool's request, its credit note when it may charge, and the guidance it relies on; read that guidance with `guidance` before writing prompts or voice settings.
3. **Free and supplied first, samples before batches.** Use supplied material and free sources wherever they serve the plan. Before any batch of paid or slow work, make one sample and get it approved: the narration of the script's sample section, one image of the most representative scene, one generated clip, the music. Announce each paid call (tool, provider, model, why, sample or batch, credit note). After three rejected samples of one kind, stop and ask how to change direction.
4. **Narration.** Call the chosen voice tool with the script's `lines` (each with its id and `pause_after_seconds`) and a `timing_path` of `artifacts/narration_timing.json`. Apply the delivery cues: provider text, pace and pronunciation. `edge_tts` returns real word timings and needs the network; `piper_tts` works offline; `openai_tts` takes performance instructions; `elevenlabs_tts` takes stability, style and speed. Probe the result: each section within about 15% of its planned time. If the narration runs long, shorten the script rather than squeeze the pictures. A change of voice, provider or settings after the sample needs a new sample.
   Supplied speech has no synthesis timing: write the transcript's timing as a `narration_timing` record yourself (one line per transcript segment, words spread over each line when the transcript has no word times) so cuts and captions can follow it.
5. **Images.** Stock first when it serves (`wikimedia` returns real licences); generate when the look must be specific (`image_selector` routes by the operator's preference, then configured providers, free before paid). Write each prompt in three passes: a draft; a critique against the five aspects (subject, subject motion, scene, framing, camera) with confusable terms resolved and adjectives replaced by their visual causes ("generous white space, one accent colour", not "clean"); the rewrite you send. Keep consistency anchors word for word for recurring characters and worlds, but do not paste one style prefix into every prompt. Check what real places, people and objects look like before generating them. Never generate exact text.
6. **Video.** Stock from `pexels_video`, `pixabay_video`, `wikimedia` or `direct_clip_search`; generated clips from `video_selector` or a named provider. Prefer one longer clip to several short ones when the action is continuous. Motion beats get real motion; a still never silently replaces a planned clip.
7. **Music and effects.** The track agreed at the proposal: `music_library` for the operator's tracks; trimming, looping and lowering under the voice happen at the mix. The `sfx` capability may be unavailable: use effects the operator supplies, or none, and say so. If no music source exists, record that in the manifest; do not discover it at the render.
8. **Record provenance.** For every downloaded item: licence, creator and source page. For every generated item: tool, provider, model, prompt and seed. For narration: the section, the cues applied, the settings, and whether the sample was approved.
9. **Verify.** Every path exists (`media_probe`); narration covers every section; images look like one video; clip durations cover their scenes.

## The records

`asset_manifest` (`artifacts/asset_manifest.json`): every asset with id, type, path, scene, source tool, provider, model, prompt, licence, original URL, duration and resolution; narration entries carry their voice-performance record.
`narration_timing` (`artifacts/narration_timing.json`): written by the voice tool; lines and words with start and end seconds. Cuts and captions follow it.

## Workspace

`assets/images/`, `assets/video/`, `assets/audio/` (narration), `assets/music/`, `assets/sfx/`, inside `projects/<name>/`.

## Review checklist

- Every file exists, and every downloaded or generated item has its provenance.
- Narration covers every section, follows the voice plan, and matches the approved sample.
- Images and clips hold the style and the consistency anchors.
- Nothing changed provider, model or treatment without the operator's agreement.

## Approval

As the pipeline sets (usually yes). Present the assets scene by scene (a frame or thumbnail per scene, the voice sample, the music), then stop and end the turn.

## Tools

Voice: `edge_tts`, `piper_tts`, `openai_tts`, `elevenlabs_tts`. Images: `wikimedia`, `image_selector`, `openai_image`, `flux_image`, `gflow_image`. Video: `pexels_video`, `pixabay_video`, `direct_clip_search`, `video_selector`, `kling_video`, `sora_video`, `gflow_video`. Music: `music_library`. Checks: `media_probe`, `audio_probe`, `frame_sample`. Planning: `describe`, `estimate`, `capabilities`, `guidance`.
Craft: `guidance/craft/voice-performance.md`, `guidance/craft/image-generation.md`, `guidance/craft/video-generation-prompting.md`, `guidance/craft/broll-and-stock.md`, `guidance/craft/sound-design.md`.
Vendor: `guidance/vendor/text-to-speech/SKILL.md`, `guidance/vendor/elevenlabs/SKILL.md`, `guidance/vendor/flux-best-practices/SKILL.md`, `guidance/vendor/ai-video-gen/SKILL.md`, `guidance/vendor/music/SKILL.md`, `guidance/vendor/sound-effects/SKILL.md`.
