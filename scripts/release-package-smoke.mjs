// Validate npm content and version surfaces. npm ships only the launcher: the
// guidance is compiled into the facet binary and the composer ships in the
// release archive, so npm copies of either could only drift. Native
// distribution is tested by release.yml.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const read = file => JSON.parse(readFileSync(new URL(`../${file}`, import.meta.url), 'utf8'));
const manifest = read('package.json');
const lock = read('package-lock.json');

assert.equal(lock.version, manifest.version);
assert.equal(lock.packages[''].version, manifest.version);
assert.deepEqual(lock.packages[''].bin, manifest.bin);
assert.deepEqual(lock.packages[''].devDependencies, manifest.devDependencies);
assert.deepEqual(manifest.bin, { facet: 'bin/facet-cli.js' }, 'npm exposes only the facet launcher');

// Release builds stamp the version with -ldflags "-X main.Version=..."; source
// builds identify as the same release, optionally marked -dev.
const facetVersion = (...flags) => execFileSync('go', ['run', ...flags, './cmd/facet', 'version'], {
  cwd: root, encoding: 'utf8', timeout: 300000,
}).trim();
assert.equal(facetVersion('-ldflags', `-X main.Version=${manifest.version}`), `facet v${manifest.version}`);
assert.match(facetVersion(), new RegExp(`^facet v${manifest.version.replaceAll('.', '\\.')}(-dev)?$`));

// A fixed command line through the shell: Windows npm is a cmd shim, and no
// argument here is external input.
const result = spawnSync('npm pack --dry-run --json --ignore-scripts', {
  cwd: root, encoding: 'utf8', timeout: 120000, shell: true, maxBuffer: 10 * 1024 * 1024,
});
if (result.error) throw result.error;
assert.equal(result.status, 0, result.stderr);
const [pack] = JSON.parse(result.stdout);
assert.equal(pack.name, manifest.name);
assert.equal(pack.version, manifest.version);
const files = new Set(pack.files.map(file => file.path.replaceAll('\\', '/')));
// npm always adds the README; it explains that this package is only a launcher.
assert.deepEqual([...files].sort(), ['LICENSE', 'README.md', 'THIRD_PARTY_NOTICES.md', 'bin/facet-cli.js', 'package.json'],
  'npm ships the launcher, its package manifest, the README and the license notices only');
console.log(`PASS: ${manifest.name}@${manifest.version}, facet source and release version stamps, ${files.size} npm files. Native distribution and publishing are not tested here.`);
