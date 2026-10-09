---
name: facet
description: Make, edit, animate or review a video with Facet. Use for any video request (explainer, product demo, commercial, trailer, brand film, animation, montage, talking-head edit, clips from a long recording, podcast clips, dubbing, avatar presenter) and for reviewing a rendered video. Facet runs a studio process (brief, research, concepts, script, scene plan) before production, then renders with its own tools.
---

# Facet

Facet is a video studio for your agent. It brings a studio's thinking (understand the client, research, develop concepts, write and test the story, plan every scene) and then the making: narration, stock and generated media, music, editing, composition in Remotion, HyperFrames or FFmpeg, captions, mixing and review. Facet's tools are stateless and called through the `facet` MCP server (your host may prefix the names) or the `facet` command. The pipelines, stage guides, stances, craft and vendor knowledge, styles and record schemas are served on demand by `pipeline_describe` and `guidance`. If neither the tools nor the `facet` command exists, the toolkit is missing: ask the operator to install it from https://kayiik.github.io/facet/ and restart.

**Rule Zero: every production goes through a pipeline.** Pick it with `pipelines_list`, read it with `pipeline_describe`, and ask when two fit. Pipelines differ by production method (where the material comes from and how it is transformed), not by deliverable: a product teaser is an `animated-explainer`, a kinetic launch piece is `animation`, a brand film is `cinematic`. Never improvise a production outside one.

## Start: preflight

Run `capabilities` before any creative work and present it as a menu, not a dump:

- each capability as "N of M configured" (voice, stock images and video, generated images and video, music, captions, composition…), with the composition runtimes installed;
- what can be made right now, said positively;
- quick setup offers, at most two, each with what it unlocks; if the operator declines, carry on without nagging.

A complete free path always exists: an Edge or Piper voice, stock media, Remotion scenes, the operator's music. Some capabilities have no provider yet (`sfx`, `transcription`, `capture`, `avatar`): when a pipeline needs one, offer the alternative its guide gives (a supplied transcript, a supplied screen recording, a narrated pivot) or ask.

If the first message is vague ("what can you do?"), show the menu and offer three starter prompts that work with this setup. If the operator gives a video as inspiration ("make something like this"), analyse it first (the reference analyst); if they give footage to edit, it is source material for a footage pipeline. Do not confuse the two.

## The studio

| Step | Stage | Role | Record |
|---|---|---|---|
| Understand the client | `intake` | producer | `brief` (+ `source_media_review` for supplied media) |
| Research | `research` | researcher | `research_brief` (`video_analysis_brief` for a reference) |
| Concepts and plan | `proposal` | director, with the producer | `proposal_packet` |
| Story | `script` | writer | `script` |
| Scene plan | `scene_plan` | director, art director | `scene_plan` |
| Production | `assets`, `edit`, `compose` | producer, sound designer, editor | `asset_manifest`, `narration_timing`, `edit_decisions`, `render_report` |
| Review | `review` | critic | `final_review` |
| Delivery | `publish` | producer | `publish_log` |

Each pipeline lists its own stages and approval points; footage-led pipelines go from intake straight to the script, and their intake carries the plan.

**The producer is you,** in the main conversation, always. You own the operator's understanding, the plan, the approvals and the money. Lead with one **stance**, chosen by the video's main job; the pipeline gives a default: `explainer` (make a subject clear), `product` (prove a product's promise through what it visibly does), `cinematic` (emotion, atmosphere, payoff), `editorial` (the story in supplied recordings, never falsified), `animation` (motion that communicates). Read it with `guidance` (`guidance/stances/explainer.md` and its siblings). Borrow techniques from other stances, never their promises.

For each consequential decision: state the intended effect on the audience, tie it to evidence or a stated assumption, confirm it can be made with what is configured, and keep the rejected alternatives. Infer ordinary creative details; never infer credentials, permission to spend, private access or permission to publish.

**Separate agents.** Where the host supports them, run three roles as their own agents: `facet-researcher` (research, with web access, in its own context), `facet-critic` (reviews work it did not write), `facet-reference-analyst` (reference videos). Give each the brief, the paths and what to return; it writes its record and returns one result. Approval conversations never happen inside them. Where the host has no separate agents, play the role yourself from the same guidance.

## Working a stage

1. Before each stage, call `pipeline_describe` with the pipeline and the stage. It returns the stage guide, this pipeline's notes for the stage, its review focus and success list, and live provider availability. Read any further guidance it points to with `guidance` (craft, runtimes, vendor knowledge) before calling a generation tool.
2. Do the work and write the stage's record to `artifacts/<record>.json`, following its schema in schemas/artifacts (read it with `guidance`).
3. Self-review against the review focus and success list; fix critical findings; at most two rounds, then carry on with warnings recorded.
4. At an approval point, present the record in plain language (the decision, the options, your recommendation), then stop and end the turn. Approval is per stage: an earlier "go ahead" does not cover a later gate. Without an answer, do not continue.

## Decisions and money

- **Announce every paid call before making it:** the tool, provider, model, why, whether it is a sample or a batch, and its credit note ("uses your OpenAI API credits"). Give no price unless the provider publishes one per call.
- **The operator's yes or no is the only gate.** Paid tools run through MCP, where the host asks before each call; not through the shell. Missing keys or exhausted credits are reported plainly, with the free path offered beside them.
- **Sample before any paid or slow batch:** one narration section, one image, one clip, approved first.
- **Ask before changing** a provider, model, `render_runtime`, `composition_mode` or treatment (motion to stills), or dropping narration or music. Never substitute silently, never relabel a downgrade as the original treatment. When something blocks, say what was attempted, what failed, whether it is setup, provider access, a tool fault or the design, the options, and your recommendation; then wait.
- **Engine:** when both Remotion and HyperFrames are installed, present both (fit and trade-off for this brief, your recommendation) before locking `render_runtime`; when only one is installed, say so.
- **Composition mode:** present templated (stock scenes and compositions: fast, reliable, alike) and atelier (a composition authored for this piece; recommended for hero, marketing, launch and brand work; it costs more iteration, say so).
- **Delivery promise:** record `delivery_promise` in the proposal (what was promised, whether real motion is required, any approved fallback); it is checked before rendering.
- A paid call cut off midway may already be charged and still running: ask, then rerun it with its `provider_job_id` as `resume_job_id`, or use the `recovery_path` it returned.

## Tools

Call Facet's tools through MCP, or in a shell with `facet tools run <tool> --input <json>`. Never write code or scripts to call a tool. Use `describe` for a tool's request and result (field names differ between tools; unknown fields are refused) and `estimate` before long renders; when an estimate reports `prefer_shell`, render from the shell, in the background where the host allows. Planning tools: `tools_list`, `describe`, `estimate`, `capabilities`, `pipelines_list`, `pipeline_describe`, `guidance` (also `facet tools list|describe|estimate`, `facet capabilities`, `facet pipelines list`, `facet pipelines describe <name> --stage <stage>`, `facet guidance <path>`).

## Narration, captions and the render

- **Narration with timing:** give the voice tool (`edge_tts`, `piper_tts`, `openai_tts`, `elevenlabs_tts`) the script as `lines` (`[{"id":"l1","text":"…","pause_after_seconds":0.3}]`) and a `timing_path`; it assembles one narration file and writes a `narration_timing` record with line and word times (Edge gives real word timings).
- **Cuts follow the voice:** in `edit_decisions`, a cut can take `lines: ["l3","l4"]` instead of seconds.
- **Captions:** in the composition, `captions` with the `timing_path`, `words_per_page`, `highlight_color` and `position`; as files, `subtitle_gen` from the `timing_path` (`layout` `horizontal` or `vertical`) and `ffmpeg_caption_burn` for burned subtitles.
- **Sound:** music is lowered under the narration (`duck_under_narration: true`) with every engine; `audio_mix` handles music sections, effect cues, target duration and `loudness_target` (−14 LUFS by default).
- **Render:** `video_compose` takes the edit decisions with `render_runtime`, `composition_mode`, `renderer_family`, `delivery_promise`, the cuts, `captions`, `audio`, a `theme` (a style id) and the frame size; atelier work passes `bespoke` (a project-relative entry and composition id) for Remotion, or a HyperFrames workspace. It normalises loudness after rendering.

## Review and delivery

- The critic reviews the concepts, the script, the cut and the final video, each against that stage's review focus: specific, pointing at a field, line, timestamp or frame, with severity critical, suggestion or nitpick, and a fix for every critical finding.
- Run `script_check` after the script, `plan_check` after the scene plan and on the cut, and `output_review` after rendering.
- A zero exit code is not acceptance. Give `output_review` the explicit expected profile (width, height, fps), the duration and its tolerance, the codec, the pixel format and audio presence; checks you omit are reported `assumed`, not verified. Add its contact sheet and the narration and caption coverage, then look at the frames yourself.
- Deliver with provenance: sources and their licences, music, voice, providers, every paid call, and the known limits. Facet does not upload; posting is the operator's act.

## Workspace

One folder per production, `projects/<name>/` (kebab-case from the title):

- `artifacts/`: the records, one per stage, plus the critic's reviews;
- `assets/images/`, `assets/video/`, `assets/audio/`, `assets/music/`, `assets/sfx/`: media;
- `renders/`: the deliverables;
- `review/`: contact sheets, frames and review evidence.

Pass explicit output paths inside the project to every tool.
