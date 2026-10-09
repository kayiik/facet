# Stage: intake

**Run by:** the producer, in the main conversation.
**Records:** `brief`; also `source_media_review` when the operator supplies footage, audio or images.

Understand the client before anything is made. A vague brief produces a generic video, however good the tools are.

## Inputs

- The operator's request, in their own words.
- Anything supplied: footage, audio, images, a script, a brand kit, a website or product, a music track.
- A reference video ("make something like this"). Run the reference analysis first (the `facet-reference-analyst` agent, or yourself from `guidance/stages/research.md`); its `video_analysis_brief` answers most questions about tone, pacing and structure. A reference is inspiration; footage to be edited is source material. Do not confuse them.

## Method

1. **Start from what was said.** List what the request already answers. Ask only for what is missing, one or two questions at a time, the most important first (usually purpose, then audience). It is a conversation, not a form. When the brief is detailed, summarise it in one or two sentences and name the gaps.
2. **Cover seven things**, marking each as stated, inferred or unknown:
   - purpose: teach, sell, inspire, document, entertain;
   - audience: who watches and what they already know;
   - platform: where it plays, which sets the frame size and the length;
   - tone: what it should feel like;
   - references: videos or looks they admire;
   - outcome: what the viewer should do or feel afterwards;
   - constraints: deadline, must-include, must-avoid, and whether paid steps are welcome.
   Keep the operator's own words where they reveal intent.
3. **Platform defaults**, when the operator has no preference: TikTok, Reels and Shorts at 1080×1920, 15 to 60 s; YouTube at 1920×1080, 60 to 180 s for an explainer; LinkedIn 60 to 120 s, landscape or square. Say which defaults you assumed.
4. **Inspect everything supplied before planning.** Never infer content from a file name. For each file run `media_probe` (duration, size, frame rate, audio streams); for video also `scene_detect` and `frame_sample`, and look at the frames. Note what is actually in it, the quality risks (low resolution, mono or noisy audio, shaky or dark footage, burned-in text, framing that will not survive a vertical crop) and what it can be used for.
   Spoken material needs a transcript for selection, captions and translation. The `transcription` capability may be unavailable: ask for a transcript or subtitle file (SRT or VTT) the operator already has, and say plainly what can and cannot be planned without one.
5. **Choose the pipeline and the stance.** Match the request by production method (where the material comes from and how it is transformed) using `pipelines_list`; read the choice with `pipeline_describe`; ask when two fit. The stance follows the video's main job; the pipeline gives a default.
6. **Restate the brief** in two or three sentences: what, for whom, where, how long, how it should feel, what the viewer takes away. Invite correction.

## The records

`brief` (`artifacts/brief.json`): title; a working hook; key points as concrete claims, not topics; the core message, one sentence the viewer remembers tomorrow; call to action; tone; style direction; target audience; target platform; target duration; reference material. Inferred and unknown answers are marked, never invented.

`source_media_review` (`artifacts/source_media_review.json`): one entry per file, marked reviewed, with its technical probe, content summary, transcript summary when there is one, representative frames, quality risks and what it is usable for; then an overall summary and the planning implications (what the material supports, what it cannot carry, what is missing).

## When intake is also the approval point

Pipelines without research and proposal stages (most footage-led ones, avatar and localization) go from intake straight to the script. Their intake ends with the plan a proposal would otherwise give, recorded in the brief's metadata:

- the treatment, with two or three options when the material can go different ways, and your recommendation;
- the outputs: how many, frame sizes, durations, platforms;
- `render_runtime`, `composition_mode`, `renderer_family` and `delivery_promise`, presented as in `guidance/stages/proposal.md` (both runtimes when both are installed and usable for this pipeline);
- captions, voice and music; paid steps with their credit notes; what needs a capability that is unavailable, with the alternative.

Present it, then stop and end the turn. Pipelines that research first do not stop at intake: restate the brief and continue unless the operator corrects you.

## Review checklist

- Purpose, audience, platform, length and tone are stated or marked as assumed.
- Key points are claims; the core message is one sentence.
- Every supplied file was inspected, and the plan relies only on what is in it.
- Nothing the operator did not say is presented as their wish.

## Avoid

- A numbered questionnaire, or asking what was already answered.
- Assuming an explainer. Listen for cinematic, animation, product or footage-led work.
- Holding up a clear brief with questions research can answer.

## Tools

Planning: `capabilities` (preflight), `pipelines_list`, `pipeline_describe`, `guidance`.
Media: `media_probe`, `audio_probe`, `scene_detect`, `frame_sample`.
Craft: `guidance/craft/storytelling.md`, `guidance/craft/short-form.md`, `guidance/craft/long-form.md`, `guidance/craft/reference-analysis.md`.
