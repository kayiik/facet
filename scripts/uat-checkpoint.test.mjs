import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { EventEmitter } from 'node:events';
import { atomicJSON, runWithCheckpoints } from './uat-checkpoint.mjs';
import { evidenceFiles } from './uat-journey.mjs';

test('runner timeout evidence uses the deadline clock and drains export before return', async () => {
  let tick = 10, deadline, interval, exported = false;
  const cleared = [];
  const child = new EventEmitter();
  child.kill = signal => { assert.equal(signal, 'SIGKILL'); child.emit('close', null, signal); };
  const runtime = {
    now: () => tick, spawn: () => child,
    setTimeout: (fn, ms) => { assert.equal(ms, 100); deadline = fn; return 'deadline'; },
    setInterval: fn => { interval = fn; return 'interval'; },
    clearTimeout: id => cleared.push(id), clearInterval: id => cleared.push(id)
  };
  const pending = runWithCheckpoints('synthetic', [], { timeout: 100, runtime, checkpoint: async () => { await Promise.resolve(); exported = true; } });
  tick = 110;
  interval();
  deadline();
  const result = await pending;
  assert.equal(result.timed_out, true);
  assert.equal(result.timeout_ms, 100);
  assert.equal(result.timeout_elapsed_ms, 100);
  assert.equal(result.elapsed_ms, 100);
  assert.equal(result.exit_code, null);
  assert.equal(exported, true);
  assert.deepEqual(cleared, ['interval', 'deadline']);
});

test('atomic JSON replaces valid snapshots without retaining intermediate files', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-atomic-'));
  try {
    const file = path.join(dir, 'progress.json');
    atomicJSON(file, { step: 1 });
    atomicJSON(file, { step: 2 }, text => text.replace('2', '3'));
    assert.deepEqual(JSON.parse(fs.readFileSync(file)), { step: 3 });
    assert.deepEqual(fs.readdirSync(dir), ['progress.json']);
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});

test('synthetic running child exports durable partial evidence before timeout, never after return', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-child-'));
  let exports = 0, active = 0, maxActive = 0;
  try {
    const result = await runWithCheckpoints(process.execPath, ['-e', 'setInterval(()=>{},1000)'], {
      timeout: 200, intervalMs: 10,
      checkpoint: async () => {
        active++;
        maxActive = Math.max(maxActive, active);
        await delay(25);
        atomicJSON(path.join(dir, 'partial.json'), { status: 'running', final: false, adjudication: false, sequence: ++exports });
        active--;
      }
    });
    assert.match(result.error, /timed out/);
    assert.notEqual(result.exit_code, 0);
    assert.equal(maxActive, 1);
    assert.ok(exports >= 2, 'Checkpoints must execute while child is alive');
    assert.equal(result.checkpoint_failed, false);
    const retained = fs.readFileSync(path.join(dir, 'partial.json'), 'utf8');
    assert.equal(JSON.parse(retained).adjudication, false);
    await delay(60);
    assert.equal(fs.readFileSync(path.join(dir, 'partial.json'), 'utf8'), retained);
    assert.equal(active, 0);
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});

test('child exit, spawn failure and export failure remain failures; last export is awaited', async () => {
  let exported = false;
  const success = await runWithCheckpoints(process.execPath, ['-e', 'process.exit(0)'], {
    timeout: 2000, checkpoint: async () => { await delay(10); exported = true; }
  });
  assert.equal(success.exit_code, 0);
  assert.equal(success.timed_out, false);
  assert.equal(success.timeout_elapsed_ms, null);
  assert.equal(exported, true);
  const failed = await runWithCheckpoints(process.execPath, ['-e', 'process.exit(7)'], {
    timeout: 2000, checkpoint: () => { throw new Error('synthetic private error not exported'); }
  });
  assert.equal(failed.exit_code, 7);
  assert.equal(failed.checkpoint_failed, true);
  assert.ok(!JSON.stringify(failed).includes('private'));
  const absent = await runWithCheckpoints('nonexistent-uat-synthetic-command', [], { timeout: 2000, checkpoint: () => {} });
  assert.match(absent.error, /Runner failed/);
  assert.equal(absent.timed_out, false, 'Spawn failure is not a deadline expiry');
});

test('evidence allowlist names only plain journey files', () => {
  assert.ok(evidenceFiles.includes('progress.json') && evidenceFiles.includes('checks.json') && evidenceFiles.includes('final.mp4'));
  assert.equal(new Set(evidenceFiles).size, evidenceFiles.length);
  assert.ok(evidenceFiles.every(name => !/private|writing|copying|[/\\]/.test(name)));
});
