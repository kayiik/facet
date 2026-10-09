import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const version = JSON.parse(read('package.json')).version;
const escaped = version.replaceAll('.', '\\.');

test('package.json names the 0.0.1 release', () => {
  assert.equal(version, '0.0.1');
});

test('shipping version surfaces identify the package.json release', () => {
  const exact = {
    'package-lock.json': new RegExp(`"version": "${escaped}"`),
    // Release builds stamp main.Version with -ldflags; source builds may add -dev.
    'cmd/facet/main.go': new RegExp(`Version = "${escaped}(-dev)?"`),
    'installer/manifest.tsv': new RegExp(`^release\\tfacet\\t${escaped}\\t`, 'm'),
    '.release-harness/docker/Dockerfile': new RegExp(`^ARG FACET_VERSION=${escaped}$`, 'm'),
  };
  for (const [path, pattern] of Object.entries(exact)) {
    assert.match(read(path), pattern, `${path} does not identify v${version}`);
  }
});

test('published installer checks run only as explicit post-release gates', () => {
  const workflow = read('.github/workflows/bootstrap.yml');
  assert.match(workflow, /name: Verify pinned published installer and handoff\s+if: github\.event_name == 'workflow_dispatch'/);
  assert.match(workflow, /name: Published one-liner installs the facet runtime[^\n]*\s+if: github\.event_name == 'workflow_dispatch'/);
});

test('releases are drafted from tags and never replace published assets', () => {
  const workflow = read('.github/workflows/release.yml');
  assert.match(workflow, /if: startsWith\(github\.ref, 'refs\/tags\/v'\)/);
  assert.match(workflow, /gh release create "\$GITHUB_REF_NAME" --verify-tag --draft /);
  assert.doesNotMatch(workflow, /--clobber|gh release upload|gh release edit/);
});
