# Short-form vertical

TikTok, Reels and Shorts are watched on a phone, held upright, often muted, by viewers who decide to stay or scroll within two seconds. Everything follows from that: a 9:16 frame, the hook in the first second, captions on, something changing every one to three seconds, and nothing important under the platform's buttons. Hook and pacing numbers are in `guidance/craft/hooks-and-pacing.md`, caption rules in `guidance/craft/captions.md`, type sizes in `guidance/craft/typography.md`.

## Format

- 1080×1920 at 30 fps: `width`, `height` and `fps` in `edit_decisions`; H.264 MP4 out of `video_compose`.
- `Explainer` at vertical size carries most pieces; `TalkingHead` a presenter with captions; `ProductRevealVertical` a short product reveal.
- Source every asset in portrait: `pexels_video` with `orientation: "portrait"`, generated video in `portrait` or 9:16, generated stills in 9:16. A 9:16 crop from 1080p landscape footage is too soft; crop only from 4K.

## Length and shape

| Length | Carries | Shape |
|---|---|---|
| 15 s | one fact, one tip, one visual gag | hook 0–1 s; context to 3 s; the content to 12 s; payoff or call to action to 15 s |
| 30 s | one concept, a before and after | hook 0–1 s; the problem to 5 s; the solution in steps, a visual change every 2 to 3 s, to 22 s; the result to 28 s; the call to action |
| 60 s | a mini tutorial, a short story | the finished result first, 0–2 s; "here's how, in N steps" to 8 s; 3 to 5 steps of about 8 s; the result at 45–55 s; the call to action and a loop back to the start |

Shorter is finished more often. Go past 60 s only when the structure holds attention all the way: a question that stays open, numbered steps, a reveal near the end.

Narration runs at 180 to 200 words a minute: about 38 to 45 words for 15 s, 75 to 90 for 30 s, 155 to 180 for 60 s. Give `script_check` that pace; its default is 150.

## The first two seconds

- Frame one moves and is worth looking at. No logo, no blank title, no "hey guys".
- The hook is on screen in words within 0.5 s and spoken at once; the voice starts on the first frame.
- Music, when used, starts on the first frame too.
- Make the hook its own narration line and tie the first cut to it (`lines: ["l1"]`).

## Safe areas

Platform buttons, captions and descriptions cover the edges of a 1080×1920 frame. The centred 900×1400 px area is clear on every platform; when the video is for one platform, its own margins are:

| Platform | Top | Bottom | Right |
|---|---|---|---|
| TikTok | 108 px | 320 px | 120 px |
| Instagram Reels | 210 px | 310 px | 84 px |
| YouTube Shorts | 120 px | 300 px | 96 px |
| Facebook Reels | 100 px | 300 px | 60 px |

- Faces, products, text and the point of interest stay inside the safe area; the right column holds the like and share buttons.
- On-screen text: 3 to 5 words a block, in the upper 40% of the safe area, bold, with a scale pop of 0.2 to 0.3 s.
- Captions sit lower in the safe area, above the bottom margin, never under the buttons.

## Captions are always on

Many viewers watch muted. Draw them in the composition (`captions` with the `timing_path`, `words_per_page: 3`, the style's `highlight_color`) or burn them with `ffmpeg_caption_burn` from `subtitle_gen` cues made with `layout: "vertical"`. Bold sans-serif, 56 to 80 px, at most two lines; white with a dark outline or a box. When the captions are the main text of the piece, `position: "center"` is allowed; keep them clear of on-screen text either way.

## Pace

- A visual change every 1 to 3 s: a cut, a zoom, a text pop, a new element. No static hold over 3 s.
- Shots of 1.5 to 4 s; 20 to 40 cuts a minute.
- Text blocks hold 2 to 4 s; a key number 3 s.
- Transitions are hard cuts or quick moves of 0.3 to 0.5 s.
- No dead air; pauses stay short (0.2 to 0.4 s between lines).
- Speed up dull setup footage 1.2 to 1.5 times with `video_trimmer`; never speed up speech.

## Sound

The voice leads, music sits well under it from the first frame, and short effects mark text pops and transitions. Energetic pieces suit 120 to 140 BPM, explainers 90 to 110. The final mix is −14 LUFS with true peak at or below −1 dBTP (`audio_mix` and `video_compose` normalise to it). Levels in `guidance/craft/sound-design.md`.

## The ending

The call to action comes in the last 5 s, one specific action, on screen and spoken. Nothing new in the landing. Let the last frame lead back into the first (the same image, or a line that sets up the opening) so a replay feels seamless.

## Cutting down from long-form

1. Pick one idea that stands on its own; a vertical cut is not a trailer for the long video.
2. Write a new hook for it; the original introduction is never the hook.
3. Reframe with `source_edit`: `fit: "cover"` and a `focal_point` on the subject, a new segment whenever the subject moves across the frame. Or keep the landscape picture in a centre band (`fit: "contain"`) with the title above and captions below.
4. Build the captions from the new narration's timing record, or from subtitles the operator supplies for the kept speech.
5. Rebuild the pace: shorter shots, faster changes, a new ending.

## Quality checks

- 1080×1920 at 30 fps (`output_review` with the expected profile).
- Frame one moves; the hook is in words within 0.5 s and spoken at once.
- Captions cover all speech, 3 to 4 words a page, inside the safe area, clear of the buttons and of on-screen text.
- Nothing important in the platform margins; check frames at phone size with `frame_sample` or the `output_review` `contact_sheet`.
- Something changes every 1 to 3 s; no hold over 3 s.
- The length matches the plan; the call to action lands in the last 5 s; the end leads back to the start.
- Loudness at −14 LUFS; music never covers the voice.
