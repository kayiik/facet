import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// TEST ONLY. Facet 2.0 is a CLI and serves nothing over HTTP. The release
// harness's scenario runner still drives a browser, so the UAT container serves
// this read-only page: what the installed runtime reports about itself and the
// latest journey summary. It never runs tools or accepts input.
const home = process.env.HOME || '/home/facet';
const facet = path.join(home, '.facet', 'current', 'bin', 'facet');
const summaryFile = path.join(home, 'uat-evidence', 'journey', 'checks.json');

const escape = value => String(value).replace(/[&<>"']/g, char => `&#${char.charCodeAt(0)};`);

export function statusPage({ version, runtime, checks }) {
  const identity = /^facet v\d+\.\d+\.\d+\S*$/.test(version ?? '')
    ? `<p data-uat="installed-version">${escape(version)}</p>`
    : `<p data-uat="installation-error">Installed runtime did not report a version: ${escape(version ?? 'unavailable')}</p>`;
  const rows = (checks ?? []).map(check => `<li data-uat-check="${escape(check.name)}">${escape(check.name)}: ${check.passed ? 'passed' : 'FAILED'}</li>`).join('');
  return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Facet CLI UAT</title>'
    + '<style>body{font:16px/1.5 system-ui,sans-serif;max-width:760px;margin:40px auto;padding:0 16px;color:#1f2328}code{overflow-wrap:anywhere}</style></head>'
    + '<body><h1>Facet CLI UAT</h1><p>Test-only status page. Facet itself serves no HTTP surface.</p>'
    + `${identity}<p>Active runtime: <code data-uat="runtime">${escape(runtime ?? 'unavailable')}</code></p>`
    + `<h2>Latest journey</h2>${rows ? `<ul>${rows}</ul>` : '<p data-uat="journey-pending">Not run yet.</p>'}</body></html>`;
}

function installedIdentity() {
  let version = null, runtime = null;
  try { version = execFileSync(facet, ['version'], { encoding: 'utf8', timeout: 30000, stdio: ['ignore', 'pipe', 'pipe'] }).trim(); } catch { /* Reported on the page. */ }
  try { runtime = fs.realpathSync(path.join(home, '.facet', 'current')); } catch { /* Reported on the page. */ }
  return { version, runtime };
}

function latestChecks() {
  try {
    const summary = JSON.parse(fs.readFileSync(summaryFile, 'utf8'));
    return summary.final === true && Array.isArray(summary.checks) ? summary.checks.map(({ name, passed }) => ({ name, passed: passed === true })) : [];
  } catch { return []; }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const identity = installedIdentity();
  const server = http.createServer((request, response) => {
    const headers = { 'cache-control': 'no-store', 'x-content-type-options': 'nosniff', 'content-security-policy': "default-src 'none'; style-src 'unsafe-inline'" };
    if (request.method !== 'GET' || new URL(request.url, 'http://uat.invalid').pathname !== '/') {
      response.writeHead(404, { ...headers, 'content-type': 'text/plain; charset=utf-8' });
      response.end('Not found');
      return;
    }
    response.writeHead(200, { ...headers, 'content-type': 'text/html; charset=utf-8' });
    response.end(statusPage({ ...identity, checks: latestChecks() }));
  });
  server.listen(8788, '0.0.0.0');
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)));
}
