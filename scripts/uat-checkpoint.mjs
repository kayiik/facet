import fs from 'node:fs';
import { spawn } from 'node:child_process';

export function atomicJSON(file, value, redact = text => text) {
  const temporary = `${file}.writing`;
  fs.writeFileSync(temporary, redact(JSON.stringify(value, null, 2)), { mode: 0o600 });
  fs.renameSync(temporary, file);
}

// One child, one non-overlapping checkpoint callback. Drain the last export
// before returning so no timer can mutate evidence after the caller seals it.
export async function runWithCheckpoints(command, args, { timeout, checkpoint, intervalMs = 30000, runtime = { now: () => performance.now(), spawn, setInterval, clearInterval, setTimeout, clearTimeout }, ...options }) {
  const started = runtime.now();
  const child = runtime.spawn(command, args, { stdio: 'ignore', ...options });
  let failure, exporting = null, checkpointFailed = false, timedOut = false, timeoutElapsedMs = null;
  const publish = () => {
    if (!exporting) exporting = Promise.resolve().then(checkpoint).catch(() => { checkpointFailed = true; }).finally(() => { exporting = null; });
    return exporting;
  };
  const timer = runtime.setInterval(publish, intervalMs);
  const deadline = runtime.setTimeout(() => { timedOut = true; timeoutElapsedMs = runtime.now() - started; failure = 'Runner failed or timed out'; child.kill('SIGKILL'); }, timeout);
  const result = await new Promise(resolve => {
    child.on('error', () => { failure = 'Runner failed or timed out'; });
    child.on('close', (code, signal) => resolve({ exit_code: code, signal, error: failure }));
  });
  runtime.clearInterval(timer);
  runtime.clearTimeout(deadline);
  await exporting;
  await publish();
  return { ...result, timed_out: timedOut, timeout_ms: timeout, timeout_elapsed_ms: timeoutElapsedMs, checkpoint_failed: checkpointFailed, elapsed_ms: runtime.now() - started };
}
