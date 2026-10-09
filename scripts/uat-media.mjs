import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';
import { createHash } from 'node:crypto';

// Decode one frame and measure audio energy: proves the delivered file carries
// decodable pictures and an audible track, not merely valid container metadata.
export function decodedMedia(file, { sampleSeconds = 1, minMeanDb = -45 } = {}) {
  const options = { timeout: 120000, maxBuffer: 32 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] };
  const frame = execFileSync('ffmpeg', ['-v', 'error', '-i', file, '-ss', String(sampleSeconds), '-map', '0:v:0', '-frames:v', '1', '-pix_fmt', 'rgb24', '-f', 'rawvideo', '-'], options);
  if (!frame.length) throw new Error('No decoded video frame at sample time');
  // volumedetect writes its measured energy to stderr, not stdout.
  const volume = spawnSync('ffmpeg', ['-hide_banner', '-nostats', '-i', file, '-map', '0:a:0', '-vn', '-af', 'volumedetect', '-f', 'null', '-'], { ...options, encoding: 'utf8' });
  if (volume.error || volume.status !== 0) throw new Error('Audio energy measurement failed');
  const match = volume.stderr.match(/mean_volume:\s*(-?\d+(?:\.\d+)?|-inf)\s*dB/);
  const meanDb = match ? Number(match[1]) : NaN;
  if (!Number.isFinite(meanDb) || meanDb < minMeanDb) throw new Error('Audio energy below required mean-volume threshold');
  return { decoded_frame_sha256: createHash('sha256').update(frame).digest('hex'), sample_seconds: sampleSeconds, mean_volume_db: meanDb, min_mean_volume_db: minMeanDb };
}

export function inspectMedia(file, { minDuration = 0.1, maxDuration = 600, audio = true } = {}) {
  if (!statSync(file).isFile()) throw new Error('Artifact is not a regular file');
  const facts = JSON.parse(execFileSync('ffprobe', ['-v', 'error', '-show_format', '-show_streams', '-of', 'json', file], { encoding: 'utf8', timeout: 30000, stdio: ['ignore', 'pipe', 'pipe'] }));
  const duration = Number(facts.format?.duration);
  if (!Number.isFinite(duration) || duration < minDuration || duration > maxDuration) throw new Error('Artifact duration outside required range');
  const video = facts.streams?.find(s => s.codec_type === 'video');
  const sound = facts.streams?.find(s => s.codec_type === 'audio');
  if (video?.codec_name !== 'h264' || !(video.width > 0) || !(video.height > 0)) throw new Error('Expected H.264 video with nonzero dimensions');
  if (audio && sound?.codec_name !== 'aac') throw new Error('Expected AAC audio');
  execFileSync('ffmpeg', ['-v', 'error', '-xerror', '-i', file, '-map', '0:v:0', ...(audio ? ['-map', '0:a:0'] : []), '-f', 'null', '-'], { timeout: 120000, stdio: ['ignore', 'pipe', 'pipe'] });
  return { ...facts, sha256: createHash('sha256').update(readFileSync(file)).digest('hex'), bytes: statSync(file).size, full_decode: true };
}
