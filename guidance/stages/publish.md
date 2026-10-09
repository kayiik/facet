# Stage: publish

**Run by:** the producer.
**Record:** `publish_log`.

Delivery: the operator gets the finished video, everything needed to post it, and an honest account of how it was made. Facet does not upload anything; posting is the operator's act.

## Inputs

`render_report`, `final_review`, `asset_manifest`, the plan (`proposal_packet` or `brief`), `script`.

## Method

1. **Assemble the delivery package** in `renders/`:
   - the final video or videos, named by what they are (platform, ratio, language, clip number);
   - caption files (SRT or VTT from `subtitle_gen`) for platforms that take them, even when captions are burned in;
   - a thumbnail or poster frame: the strongest frame from `frame_sample` (the opening frame often doubles as the thumbnail on social platforms), or a designed one if that was agreed;
   - the metadata text and the credits (below).
2. **Write the metadata for each platform.** Title: specific, under 60 characters for YouTube, leading with the hook or the number. Description: the first 150 characters carry the hook and the value; then what is covered, chapters (from the script sections' start times), sources, and one call to action. Five to ten specific tags; three to five hashtags for social platforms. For a batch of clips, a title per clip and a suggested posting order.
3. **Write the credits and provenance:** every source with its licence, creator and page (attribution lines where the licence requires them), the music and its licence, the voice used, the providers used, and the paid calls made (tool, provider, model, how many).
4. **State the known limits:** anything simplified, substituted with the operator's agreement, or left as a warning by the final review.
5. **Present for acceptance:** the video path, the review summary (status, duration, frame size, loudness, coverage), the provenance and the limits. Changes go back to the stage that can make them.

## The record

`publish_log` (`artifacts/publish_log.json`): one entry per deliverable and platform with status exported, the export path, the time, and the metadata used (title, description, hashtags, chapters).

## Review checklist

- Every deliverable is in the package with its captions, thumbnail and metadata.
- Every downloaded item is credited with its licence; every paid call is listed.
- The final review passed, or its open warnings are stated.
- Metadata fits each target platform.

## Approval

Always: the operator accepts the delivery or asks for changes. Then stop and end the turn.

## Tools

`frame_sample`, `subtitle_gen`, `media_probe`; planning: `guidance`.
Craft: `guidance/craft/short-form.md`, `guidance/craft/long-form.md`, `guidance/craft/captions.md`.
