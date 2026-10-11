import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const version = JSON.parse(read('package.json')).version;
const escaped = version.replaceAll('.', '\\.');

test('package.json names the 0.0.2 release', () => {
  assert.equal(version, '0.0.2');
});

test('shipping version surfaces identify the package.json release', () => {
  const exact = {
    'package-lock.json': new RegExp(`"version": "${escaped}"`),
    // Release builds stamp main.Version with -ldflags; source builds may add -dev.
    // The App and the Toolkit share one version.
    'cmd/facet/main.go': new RegExp(`Version = "${escaped}(-dev)?"`),
    'cmd/facet-app/main.go': new RegExp(`Version = "${escaped}(-dev)?"`),
  };
  for (const [path, pattern] of Object.entries(exact)) {
    assert.match(read(path), pattern, `${path} does not identify v${version}`);
  }
});

test('a release is drafted from a tag on main and never replaces a published one', () => {
  const workflow = read('.github/workflows/release.yml');
  assert.match(workflow, /^ {4}tags: \['v\*'\]$/m);
  assert.match(workflow, /"\$GITHUB_REF_NAME" != "v\$version"/, 'the tag must name package.json\'s version');
  assert.match(workflow, /git merge-base --is-ancestor "\$GITHUB_SHA" origin\/main/, 'the tag must be on main');
  assert.match(workflow, /name: Draft the release\s+if: github\.event_name == 'push'/, 'a dry run must not release');
  assert.match(workflow, /needs: \[build, smoke, uat\]/, 'no draft without the smoke tests and the end-to-end test');
  assert.match(workflow, /-F draft=true/);
  assert.match(workflow, /a published release is never replaced/);
  assert.doesNotMatch(workflow, /--clobber|draft=false|make_latest|gh release edit|branches:/);
});
