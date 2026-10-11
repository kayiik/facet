#!/usr/bin/env node
'use strict';

// npm supplies this launcher, not Facet: it runs a facet already installed on
// this computer and never installs one. It never runs itself: a `facet` on
// PATH that is a script, such as npm's link or shim to this file, is skipped.
const { spawn } = require('child_process');
const path = require('path');
const os = require('os');
const fs = require('fs');

const MISSING = [
  'Facet is not installed. The npm package is only a launcher; install Facet once per user:',
  '  macOS/Linux: curl -fsSL https://kayiik.github.io/facet/install.sh | sh',
  '  Windows:     irm https://kayiik.github.io/facet/install.ps1 | iex',
  'Or set FACET_BIN to an installed facet executable.',
].join('\n');

// Search order: an explicit FACET_BIN, then the folder the installer installs
// into (%LOCALAPPDATA%\Programs\Facet on Windows, ~/.local/lib/facet
// elsewhere), then PATH.
function candidates() {
  const windows = os.platform() === 'win32';
  const name = windows ? 'facet.exe' : 'facet';
  const list = [];
  try {
    const home = os.homedir();
    const programs = windows
      ? path.join(process.env.LOCALAPPDATA || path.join(home, 'AppData', 'Local'), 'Programs', 'Facet')
      : path.join(home, '.local', 'lib', 'facet');
    list.push(path.join(programs, 'bin', name));
  } catch { /* No resolvable home directory. */ }
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (dir.trim()) list.push(path.join(dir, name));
  }
  return list;
}

// A program, not a script: npm's links and shims to this launcher start
// with #!.
function program(candidate) {
  let fd;
  try {
    if (!fs.statSync(candidate).isFile()) return false;
    fd = fs.openSync(candidate, 'r');
    const head = new Uint8Array(2);
    return fs.readSync(fd, head, 0, 2, 0) === 2 && !(head[0] === 0x23 && head[1] === 0x21);
  } catch {
    return false;
  } finally {
    if (fd !== undefined) fs.closeSync(fd);
  }
}

const explicit = process.env.FACET_BIN;
const bin = explicit && fs.existsSync(explicit) ? explicit : candidates().find(program);

if (!bin) {
  console.error(MISSING);
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' });

child.on('error', (err) => {
  console.error(`Failed to execute the facet binary (${bin}): ${err.message}`);
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
  } else {
    process.exit(code ?? 0);
  }
});
