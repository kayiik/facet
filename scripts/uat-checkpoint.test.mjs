import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { runInNewContext } from 'node:vm';
import { EventEmitter } from 'node:events';
import { atomicJSON, runWithCheckpoints } from './uat-checkpoint.mjs';
import { listing, runnerDiagnostics } from './uat-probe.mjs';
import { evidenceFiles } from './uat-journey.mjs';
import { processInventory } from './uat-checkpoint-process.mjs';

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

test('failed runner writes durable diagnostics even if capture fails, without copying command errors', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-runner-diagnostic-'));
  try {
    const result = { timed_out: true, exit_code: null, timeout_ms: 100, timeout_elapsed_ms: 101 };
    await runnerDiagnostics('journey', result, dir, async () => {
      await Promise.resolve();
      throw new Error('private command args/env/auth synthetic-secret');
    }, () => 0);
    const file = path.join(dir, 'journey-runner-diagnostics.json');
    const text = fs.readFileSync(file, 'utf8');
    const diagnostic = JSON.parse(text);
    assert.equal(diagnostic.observed_at, '1970-01-01T00:00:00.000Z');
    assert.equal(diagnostic.adjudication, false);
    assert.equal(diagnostic.timeout_elapsed_ms, 101);
    assert.equal(diagnostic.process_inventory.status, 'unavailable');
    assert.ok(!text.includes('synthetic-secret'));
    const inventory = { status: 'complete', processes: [{ pid: 10, ppid: 1, name: 'node', elapsedMs: 5, cpuMs: 0, rssBytes: 1024 }] };
    await runnerDiagnostics('journey', { ...result, timed_out: false, exit_code: 1 }, dir, async () => JSON.stringify(inventory), () => 0);
    assert.deepEqual(JSON.parse(fs.readFileSync(file)).process_inventory, inventory);
    assert.equal(JSON.parse(fs.readFileSync(file)).timed_out, false);
    await runnerDiagnostics('journey', { timed_out: false, exit_code: 0 }, dir, () => { assert.fail('Successful child needs no failure capture'); });
    assert.deepEqual(fs.readdirSync(dir), ['journey-runner-diagnostics.json']);
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});

test('bounded proc fixture captures owned active targets and descendants, never arbitrary names or secrets', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-proc-'));
  const fixture = (pid, ppid, name, uid = 10001, state = 'S') => {
    const dir = path.join(root, String(pid));
    fs.mkdirSync(dir);
    const fields = Array(22).fill('0');
    fields[0] = state; fields[1] = String(ppid); fields[11] = '120'; fields[12] = '30'; fields[19] = '200';
    fs.writeFileSync(path.join(dir, 'stat'), `${pid} (${name}) ${fields.join(' ')}`);
    fs.writeFileSync(path.join(dir, 'status'), `Uid:\t${uid}\t${uid}\t${uid}\t${uid}\nVmRSS:\t42 kB\n`);
  };
  try {
    fs.writeFileSync(path.join(root, 'uptime'), '12 0');
    fixture(10, 1, 'node');
    fixture(11, 10, 'secret ) arbitrary');
    fixture(12, 11, 'ffmpeg');
    fixture(13, 1, 'facet');
    fixture(14, 1, 'unrelated-secret');
    fixture(15, 10, 'node', 0);
    fixture(16, 10, 'ffmpeg', 10001, 'Z');
    fs.mkdirSync(path.join(root, '17')); // Disappeared or unreadable process metadata.
    fixture(18, 13, 'chrome-headless');
    const options = { root, uid: 10001, now: () => 0, clockTicks: 100 };
    const result = processInventory(options);
    assert.deepEqual(result.processes.map(row => row.pid), [10, 11, 12, 13, 18]);
    assert.deepEqual(result.processes[0], { pid: 10, ppid: 1, name: 'node', elapsedMs: 10000, cpuMs: 1500, rssBytes: 43008 });
    assert.equal(result.processes[1].name, 'other');
    assert.equal(result.processes[4].name, 'chrome-headless');
    assert.equal(result.skipped, 1);
    assert.equal(result.status, 'partial');
    assert.ok(!JSON.stringify(result).includes('secret'));
    assert.equal(processInventory({ ...options, maxRows: 1 }).processes.length, 1);
    assert.equal(processInventory({ ...options, maxProcesses: 1 }).scanned, 1);
    assert.equal(processInventory({ ...options, maxEntries: 0 }).truncated, true);
    let clock = 0;
    assert.equal(processInventory({ ...options, now: () => clock++, budgetMs: 1 }).truncated, true);
    assert.equal(processInventory({ ...options, root: path.join(root, 'absent') }).status, 'unavailable');
    fs.writeFileSync(path.join(root, '10', 'status'), 'x'.repeat(17000));
    assert.equal(processInventory(options).skipped, 2, 'Oversize metadata must not be read without a bound');
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
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

test('evidence allowlist names only plain journey files; export follows final checks', () => {
  assert.ok(evidenceFiles.includes('progress.json') && evidenceFiles.includes('checks.json') && evidenceFiles.includes('final.mp4'));
  assert.equal(new Set(evidenceFiles).size, evidenceFiles.length);
  assert.ok(evidenceFiles.every(name => !/private|writing|copying|[/\\]/.test(name)));
  const probe = fs.readFileSync(new URL('./uat-probe.mjs', import.meta.url), 'utf8');
  assert.ok(probe.indexOf('await runnerDiagnostics(') < probe.lastIndexOf('await exportEvidence();'), 'diagnostics are captured before the final export');
  assert.ok(probe.indexOf('result = await runWithCheckpoints(') < probe.lastIndexOf('await exportEvidence();'), 'final export happens after the journey closed');
});

test('synthetic container listing withholds unfinished evidence and rejects directories', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-listing-'));
  try {
    for (const name of ['progress.json', 'final.mp4', 'render.log', 'final-private.mp4', 'checks.json.writing']) fs.writeFileSync(path.join(dir, name), '{}');
    const list = () => {
      let output;
      runInNewContext(listing, {
        require: name => { assert.equal(name, 'node:fs'); return fs; },
        process: { argv: ['node', dir.replaceAll('\\', '/'), JSON.stringify(evidenceFiles)] },
        console: { log: text => { output = JSON.parse(text); } }
      });
      return output.map(file => file.name);
    };
    assert.deepEqual(list(), ['progress.json']);
    atomicJSON(path.join(dir, 'checks.json'), { final: false, status: 'running' });
    assert.deepEqual(list(), ['progress.json']);
    atomicJSON(path.join(dir, 'checks.json'), { final: true, status: 'complete', passed: false });
    assert.deepEqual(list(), ['progress.json', 'checks.json', 'render.log', 'final.mp4']);
    // Directories are never copied as though they were allowlisted files.
    fs.mkdirSync(path.join(dir, 'doctor.log'));
    assert.ok(!list().includes('doctor.log'));
  } finally { fs.rmSync(dir, { recursive: true, force: true }); }
});
