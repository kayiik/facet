import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { inspectMedia, decodedMedia } from './uat-media.mjs';

test('strict artifact verifier and platform wrapper reject invalid media', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-media-probe-'));
  const scripts = path.dirname(fileURLToPath(import.meta.url));
  try {
    const valid = path.join(dir, 'valid.mp4');
    execFileSync('ffmpeg', ['-v', 'error', '-y', '-f', 'lavfi', '-i', 'color=c=blue:s=320x180:r=24:d=1', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=1', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', valid], { timeout: 30000 });
    assert.equal(inspectMedia(valid).full_decode, true);
    const measured = decodedMedia(valid, { sampleSeconds: 0.5 });
    assert.ok(measured.mean_volume_db > -45);
    const remux = path.join(dir, 'remux.mp4');
    execFileSync('ffmpeg', ['-v', 'error', '-y', '-i', valid, '-c', 'copy', '-metadata', 'comment=synthetic-remux', remux]);
    assert.notEqual(inspectMedia(remux).sha256, inspectMedia(valid).sha256);
    assert.equal(decodedMedia(remux, { sampleSeconds: 0.5 }).decoded_frame_sha256, measured.decoded_frame_sha256);
    const silent = path.join(dir, 'silent-audio.mp4');
    execFileSync('ffmpeg', ['-v', 'error', '-y', '-i', valid, '-af', 'volume=0', '-c:v', 'copy', '-c:a', 'aac', silent]);
    assert.throws(() => decodedMedia(silent, { sampleSeconds: 0.5 }), /Audio energy/);
    const changed = path.join(dir, 'changed.mp4');
    execFileSync('ffmpeg', ['-v', 'error', '-y', '-i', valid, '-vf', 'negate', '-c:v', 'libx264', '-c:a', 'copy', changed]);
    assert.notEqual(decodedMedia(changed, { sampleSeconds: 0.5 }).decoded_frame_sha256, measured.decoded_frame_sha256);
    const garbage = path.join(dir, 'garbage.mp4');
    fs.writeFileSync(garbage, Buffer.alloc(4096, 65));
    const truncated = path.join(dir, 'truncated.mp4');
    fs.writeFileSync(truncated, fs.readFileSync(valid).subarray(0, 500));
    for (const file of [path.join(dir, 'missing.mp4'), garbage, truncated]) assert.throws(() => inspectMedia(file));
    assert.throws(() => inspectMedia(valid, { minDuration: 5 }));
    const command = process.platform === 'win32' ? 'pwsh' : 'bash';
    const prefix = process.platform === 'win32' ? ['-NoProfile', '-File', path.join(scripts, 'probe-artifact.ps1')] : [path.join(scripts, 'probe-artifact.sh')];
    for (const [file, expected] of [[valid, 0], [garbage, 1], [truncated, 1], [path.join(dir, 'missing.mp4'), 1]]) {
      const result = spawnSync(command, [...prefix, file], { timeout: 30000, encoding: 'utf8' });
      assert.equal(result.status, expected, `${path.basename(file)} wrapper exit`);
    }
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
