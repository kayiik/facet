import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// Diagnostic process inventory, captured inside the UAT container when a
// journey runner fails or times out (see uat-probe.mjs runnerDiagnostics).
// No cmdline, environ, executable paths, auth files, ps arguments or Docker socket.
// The worker deadline also bounds a filesystem call that does not return promptly.
export function processInventory({ root = '/proc', uid = process.getuid?.(), now = () => performance.now(), clockTicks, maxEntries = 4096, maxProcesses = 512, maxRows = 128, budgetMs = 1000 } = {}) {
  const started = now();
  const result = { status: 'complete', scope: 'same-uid-journey-tools-and-descendants', limits: { maxEntries, maxProcesses, maxRows, budgetMs }, scanned: 0, skipped: 0, truncated: false, processes: [] };
  const read = (file, limit) => {
    const fd = fs.openSync(file, 'r');
    try {
      const bytes = Buffer.alloc(limit + 1);
      const length = fs.readSync(fd, bytes, 0, bytes.length, 0);
      if (length > limit) throw new Error('Bound exceeded');
      return bytes.subarray(0, length).toString('utf8');
    } finally { fs.closeSync(fd); }
  };
  try {
    if (!Number.isSafeInteger(uid) || uid < 0) throw new Error('UID unavailable');
    clockTicks ??= Number(execFileSync('getconf', ['CLK_TCK'], { encoding: 'utf8', timeout: 1000, maxBuffer: 128, stdio: ['ignore', 'pipe', 'pipe'] }).trim());
    const uptime = Number(read(path.join(root, 'uptime'), 128).split(/\s+/)[0]);
    if (!(clockTicks > 0 && Number.isFinite(clockTicks) && uptime >= 0 && Number.isFinite(uptime))) throw new Error('Clock unavailable');
    const rows = new Map();
    const dir = fs.opendirSync(root);
    try {
      for (let entries = 0; ; entries++) {
        if (now() - started >= budgetMs || entries >= maxEntries || result.scanned >= maxProcesses) { result.truncated = true; break; }
        const entry = dir.readSync();
        if (!entry) break;
        if (!/^[1-9]\d*$/.test(entry.name)) continue;
        result.scanned++;
        try {
          const base = path.join(root, entry.name);
          const status = read(path.join(base, 'status'), 16384);
          const owners = status.match(/^Uid:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)/m);
          if (!owners || !owners.slice(1).every(value => Number(value) === uid)) continue;
          const stat = read(path.join(base, 'stat'), 4096);
          const end = stat.lastIndexOf(')');
          const start = stat.indexOf('(');
          const pid = Number(entry.name);
          if (start < 0 || end < start || Number(stat.slice(0, start).trim()) !== pid) throw new Error('Invalid stat');
          const fields = stat.slice(end + 1).trim().split(/\s+/);
          if (['Z', 'X', 'x'].includes(fields[0])) continue;
          const ppid = Number(fields[1]), userTicks = Number(fields[11]), systemTicks = Number(fields[12]), startTicks = Number(fields[19]);
          const rss = status.match(/^VmRSS:\s+(\d+)\s+kB\s*$/m);
          if (!rss || ![pid, ppid, userTicks, systemTicks, startTicks].every(value => Number.isSafeInteger(value) && value >= 0)) throw new Error('Invalid metrics');
          const name = stat.slice(start + 1, end);
          // comm is user-controlled too: export only canonical names, not arbitrary text.
          // (comm is truncated to 15 bytes, hence chrome-headless.)
          const canonical = /^(node|facet|ffmpeg|ffprobe|opencode|chromium|chrome|chrome-headless)$/.test(name) ? name : 'other';
          rows.set(pid, { pid, ppid, name: canonical, elapsedMs: Math.max(0, (uptime - startTicks / clockTicks) * 1000), cpuMs: (userTicks + systemTicks) / clockTicks * 1000, rssBytes: Number(rss[1]) * 1024 });
        } catch { result.skipped++; } // /proc entries can disappear between any two reads.
      }
    } finally { dir.closeSync(); }
    const selected = new Set([...rows.values()].filter(row => row.name !== 'other').map(row => row.pid));
    for (let changed = true; changed;) {
      changed = false;
      for (const row of rows.values()) {
        if (now() - started >= budgetMs) { result.truncated = true; changed = false; break; }
        if (!selected.has(row.pid) && selected.has(row.ppid)) { selected.add(row.pid); changed = true; }
      }
    }
    result.processes = [...rows.values()].filter(row => selected.has(row.pid)).sort((a, b) => a.pid - b.pid);
    if (result.processes.length > maxRows) { result.truncated = true; result.processes.length = maxRows; }
    if (result.truncated || result.skipped) result.status = 'partial';
  } catch { result.status = 'unavailable'; }
  result.captureElapsedMs = now() - started;
  return result;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  console.log(JSON.stringify(processInventory()));
}
