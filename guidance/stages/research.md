# Stage: research

**Run by:** the researcher: the `facet-researcher` agent where the host supports separate agents, otherwise you, from this guide.
**Records:** `research_brief`. Supplied footage is researched in `source_media_review`; a reference video in `video_analysis_brief`.

Research gathers raw material. It makes no creative decisions; the proposal turns it into concepts. It is what separates a specific, credible video from generic filler.

## Inputs

The `brief`; this pipeline's notes for the stage (`pipeline_describe` with the stage); `source_media_review` and `video_analysis_brief` when they exist.

## What research means depends on what the video starts from

- **A topic:** facts, sources, and the questions the audience really has (method below).
- **A product or website:** what it really does and can be shown doing. Visit it with the host's web tools. Record the features, the product states that prove its main promise, the user journey, the claims the brand makes and whether they hold, how competitors frame the same problem, and real user questions and complaints. Note what can be shown from the real product and what would have to be illustrated (and then labelled as illustration).
- **Supplied footage or audio:** what is actually in it: the `source_media_review` (see `guidance/stages/intake.md`), then the strongest complete moments and the context each one needs to stay truthful.
- **A music track:** its structure: duration, tempo, sections (intro, verse, chorus, drop, outro), energy peaks, where vocals and lyrics fall. List the timestamps scenes should land on. See `guidance/vendor/music-to-video/SKILL.md`.
- **A reference video:** what it does and why it works (reference analysis below), then a short pass of topic research on the operator's own subject.

## Topic research method

Search in batches, in parallel where the host allows; 10 to 25 searches in all. Add the current year or month where freshness matters, split compound topics into their parts, and vary the terms when a query returns nothing.

1. **Landscape.** The best existing videos and articles: for at least 3 (target 5 to 8), the title, source, angle, what it covers and what it misses. Note saturated angles and underserved gaps; a gap is the opportunity.
2. **Trending.** Recent news, launches, controversies and live debates (news, Reddit, Hacker News). If nothing is current, record the topic as evergreen; that is a finding too.
3. **Data.** Statistics, studies, reports, benchmarks, comparisons. Each data point: the specific claim ("73% of developers…", never "most developers…"), source URL and name, credibility (primary, secondary or anecdotal), surprise factor, and how it could be used (hook, stat scene, anchor, closing line). Minimum 3, target 5 to 8.
4. **Audience.** Real questions from forums ("why does", "ELI5", "confused"), misconceptions with the real answer, pain points, knowledge level. Sourced, never invented.
5. **Expert voices**, when the topic has them: name, affiliation, position, and whether they are contrarian.
6. **Visual references.** Two or three ways others visualise the subject, and what works in each.
7. **Angles.** At least 3 genuinely different ones (target 4 or 5). Each has a specific name, a one-sentence hook that opens an information gap, a type (trending, evergreen, contrarian, narrative, data-driven), why now (citing findings), and the findings it is grounded in. Include at least one evergreen and one surprising or contrarian angle; no two share a hook structure.
8. **Sources.** At least 5 with URLs (target 10 to 15), at least 2 primary. Flag anything older than two years.

Open the record with a one-paragraph research summary: the single most important finding. If data is thin, say so (the concepts should lean on analogy or story). If nothing exists on the topic, say so prominently.

## Reference analysis method

Work on a local file; when a link cannot be fetched, ask the operator for the file or a description. Run `media_probe`, then `scene_detect` and `frame_sample` (one frame per shot, or uniform sampling when detection fails), and look at the frames yourself. Report:

- **Content** in two sentences; **style** in one (pacing, visual treatment, energy); **structure** (scenes over seconds, average shot length, cuts per minute).
- **Motion:** how many shots are real motion, animated stills and static images. Never guess; this decides the production method.
- **Five aspects** for each shot or group of shots, each marked N/A when it does not apply: subject (type, count, attributes, how subjects appear, leave or switch); subject motion (actions in order); scene (overlays listed separately from the setting, point of view, setting, time of day); spatial framing (shot size, position, depth, and how they change); camera (speed, lens, height, angle, focus, steadiness, movement).
- Palette, typography, transitions, music, narration (voice, pace in words a minute), captions.
- **What makes it work:** two or three specific things.
- **What to keep, and seeds for differentiation.** The operator's version must not be a copy.

## The records

`research_brief` (`artifacts/research_brief.json`): the research summary; the landscape (existing content, saturated angles, underserved gaps); what is trending and how timely the subject is; data points with sources, credibility and surprise; audience insights (real questions, misconceptions with the real answer, knowledge level, pain points); expert voices; the angles with their grounding; visual references; sources.

`video_analysis_brief` (`artifacts/video_analysis_brief.json`): the source; content analysis (summary, topics, claims, audience, tone, hook technique, call to action); structure (shots with their timing, description and shot language; pacing profile); style (palette, typography, transitions, music, narration, captions, closest style); replication guidance (suggested pipeline, what to keep, what needs custom work, whether real motion is required, differentiation seeds); the sampled frames.

## Review checklist

- Data points are specific and sourced; credibility is labelled honestly.
- Audience questions come from real people.
- Angles are grounded in named findings and genuinely different.
- Claims about supplied material rest on inspection, not on file names.
- Reference analysis fills the five aspects or marks them N/A, and states the motion classification.

## Approval

None. The producer checks the record against the stage's review focus and success list, gives the operator the most important finding in two or three sentences, and moves on to the proposal.

## Tools

Web search and fetch from the host. Media: `media_probe`, `audio_probe`, `scene_detect`, `frame_sample`. Planning: `pipeline_describe`, `guidance`.
Craft: `guidance/craft/reference-analysis.md`, `guidance/craft/storytelling.md`, `guidance/craft/data-visualization.md`.
