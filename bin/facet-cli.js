#!/usr/bin/env node
'use strict';

// npm supplies this launcher, not the Facet runtime. It runs an existing native
// `facet` binary and never installs one. It never resolves `facet` through PATH
// (that would find this launcher again).
const { spawn } = require('child_process');
const path = require('path');
const os = require('os');
const fs = require('fs');

const MISSING = [
  'Facet native binary not found. The npm package is only a launcher; install the Facet runtime once per user:',
  '  macOS/Linux: curl -fsSL https://kayiik.github.io/facet/install.sh | sh',
  '  Windows:     irm https://kayiik.github.io/facet/install.ps1 | iex',
  'Or set FACET_BIN to an existing facet executable.',
].join('\n');

// Search order: an explicit FACET_BIN, then the active runtime the Facet
// installer manages in Facet's home folder: FACET_HOME when set, else ~/.facet.
function candidates() {
  const exe = os.platform() === 'win32' ? '.exe' : '';
  const list = [];
  if (process.env.FACET_BIN) list.push(process.env.FACET_BIN);
  let facetHome = (process.env.FACET_HOME || '').trim();
  if (!facetHome) {
    try { facetHome = path.join(os.homedir(), '.facet'); } catch { /* No resolvable home directory. */ }
  }
  if (facetHome) list.push(path.join(facetHome, 'current', 'bin', `facet${exe}`));
  return list;
}

const bin = candidates().find(candidate => fs.existsSync(candidate));

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
