import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { evidenceFiles, journey, renderRequest, reviewRequest } from './uat-journey.mjs';

const checks = [
  'installed-runtime-layout', 'toolchain', 'sanitizer-negative-control', 'strict-media-probe-negative-controls',
  'wire-opencode-user-scope', 'facet-doctor', 'deterministic-render', 'output-review', 'broken-runtime-pin-rejected',
];

test('render and review requests state every expectation the review verifies', () => {
  assert.deepEqual(reviewRequest.profile, { width: renderRequest.width, height: renderRequest.height, fps: renderRequest.fps });
  const end = Math.max(...renderRequest.cuts.map(cut => cut.out_seconds));
  assert.equal(reviewRequest.checks.duration.expected, end);
  assert.equal(reviewRequest.input, renderRequest.output);
  assert.equal(reviewRequest.samples.type, 'uniform');
  assert.ok(renderRequest.cuts.every(cut => ['text_card', 'hero_title', 'stat_card', 'media'].includes(cut.type)));
});

// The real journey runs in the UAT container. Here, with nothing installed in a
// scratch HOME, every product check must fail closed while final evidence is
// still published, and only allowlisted evidence may be written.
test('without an installation the journey fails closed and still publishes final evidence', { skip: process.platform === 'win32' ? 'POSIX tools required' : false }, async t => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-journey-'));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  const evidence = path.join(home, 'evidence');
  const { passed, report } = await journey(evidence, { home, version: '0.0.0-test' });
  assert.equal(passed, false);
  assert.deepEqual(report.checks.map(check => check.name), checks);
  const result = Object.fromEntries(report.checks.map(check => [check.name, check.passed]));
  for (const name of ['installed-runtime-layout', 'wire-opencode-user-scope', 'facet-doctor', 'deterministic-render', 'output-review', 'broken-runtime-pin-rejected']) {
    assert.equal(result[name], false, name);
  }
  assert.equal(result['sanitizer-negative-control'], true);
  const final = JSON.parse(fs.readFileSync(path.join(evidence, 'checks.json'), 'utf8'));
  assert.equal(final.final, true);
  assert.equal(final.status, 'complete');
  assert.equal(final.passed, false);
  assert.equal(JSON.parse(fs.readFileSync(path.join(evidence, 'progress.json'), 'utf8')).final, true);
  for (const name of fs.readdirSync(evidence)) assert.ok(evidenceFiles.includes(name), `unlisted evidence ${name}`);
});
