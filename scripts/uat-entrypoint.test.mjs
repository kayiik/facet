import { test } from 'node:test';
import assert from 'node:assert/strict';
import { statusPage } from './uat-entrypoint.mjs';

test('the test console shows the installed identity only when the runtime reported a release version', () => {
  const page = statusPage({ version: 'facet v2.0.0', runtime: '/home/facet/.facet/runtimes/2.0.0-linux-amd64', checks: [] });
  assert.match(page, /<p data-uat="installed-version">facet v2\.0\.0<\/p>/);
  assert.match(page, /data-uat="journey-pending"/);
  for (const version of [null, '', 'facet vdev', 'panic: runtime error', 'facet v2.0.0\nextra']) {
    const failed = statusPage({ version, runtime: null, checks: [] });
    assert.doesNotMatch(failed, /data-uat="installed-version"/, String(version));
    assert.match(failed, /data-uat="installation-error"/);
  }
});

test('the test console escapes every reported value', () => {
  const page = statusPage({
    version: 'facet v2.0.0-<b>',
    runtime: '/home/"x"/<script>',
    checks: [{ name: '<img src=x>', passed: true }, { name: 'render', passed: false }],
  });
  assert.ok(!page.includes('<script>') && !page.includes('<img') && !page.includes('<b>'));
  assert.match(page, /&#60;img src=x&#62;: passed/);
  assert.match(page, /render: FAILED/);
});
