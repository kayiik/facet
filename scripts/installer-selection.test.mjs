import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

// Facet 2.0 installers: one runtime per user, wiring through `facet wire`,
// nothing written into projects or CLI instruction files by the installer.
const root = new URL('../', import.meta.url);
const bash = readFileSync(new URL('install.sh', root), 'utf8');
const powershell = readFileSync(new URL('install.ps1', root), 'utf8');
const manifest = readFileSync(new URL('installer/manifest.tsv', root), 'utf8');
const pkg = JSON.parse(readFileSync(new URL('package.json', root), 'utf8'));

test('manifest describes the user-wide layout, the release, the toolchain and the components only', () => {
  assert.match(manifest, /^contract\tlayout\t2\t/m);
  assert.match(manifest, new RegExp(`^release\\tfacet\\t${pkg.version.replaceAll('.', '\\.')}\\t`, 'm'));
  for (const component of ['remotion', 'piper', 'hyperframes']) {
    assert.match(manifest, new RegExp(`^component\\t${component}\\t`, 'm'));
  }
  assert.match(manifest, /^builtin\tedge-tts\t-\tmicrosoft_edge\tall\tall\tall\t0\t/m);
  assert.doesNotMatch(manifest, /^(host|instruction|pack)\t/m, 'per-project targets and packs belong to facet wire');
});

// The installers build facet with the Go toolchain go.mod names: a newer Go in
// go.mod needs the toolchain row moved with it, with its checksums.
test('the pinned Go toolchain is the one go.mod names, with a checksum per platform', () => {
  const goVersion = readFileSync(new URL('go.mod', root), 'utf8').match(/^go (\d+\.\d+\.\d+)$/m)[1];
  const row = manifest.split('\n').map((line) => line.split('\t')).find((cols) => cols[0] === 'toolchain' && cols[1] === 'go');
  assert.ok(row, 'installer/manifest.tsv has no toolchain/go row');
  assert.equal(row[2], goVersion, 'the toolchain row must name the Go version in go.mod');
  assert.equal(row[3], 'https://go.dev/dl/');
  for (const [column, os] of [[4, 'windows'], [5, 'linux'], [6, 'darwin']]) {
    const pairs = Object.fromEntries(row[column].split(' ').map((pair) => pair.split(':')));
    assert.deepEqual(Object.keys(pairs).sort(), ['amd64', 'arm64'], `${os} lists amd64 and arm64`);
    for (const [arch, digest] of Object.entries(pairs)) {
      assert.match(digest, /^[0-9a-f]{64}$/, `${os}/${arch} SHA-256`);
    }
  }
});

test('both installers expose the same actions and options', () => {
  for (const option of ['--action', '--version', '--components', '--wire', '--scope', '--project', '--archive', '--checksums', '--from-source', '--toolchain', '--yes', '--no-path', '--skip-verify', '--purge', '--plain', '--verbose']) {
    assert.ok(bash.includes(option), `install.sh lacks ${option}`);
  }
  assert.match(bash, /install\|update\|rollback\|uninstall/);
  assert.match(powershell, /\[ValidateSet\('install','update','rollback','uninstall'\)\]\[string\]\$Action/);
  assert.match(powershell, /\[ValidateSet\('user','project'\)\]\[string\]\$Scope/);
  for (const name of ['Wire', 'ProjectDir', 'ArchivePath', 'ChecksumPath', 'ToolchainPath', 'NonInteractive', 'NoPath', 'SkipVerify', 'Purge']) {
    assert.match(powershell, new RegExp(`\\$${name}\\b`), `install.ps1 lacks -${name}`);
  }
});

// The installers build facet exactly as scripts/package-release.py does, so a
// source install reproduces the release workflow's reference build byte for
// byte: same flags, vendored modules only, the pinned toolchain, no cgo, and
// none of the user's Go settings.
test('both installers build facet from source with the packager\'s command and settings', () => {
  const packager = readFileSync(new URL('scripts/package-release.py', root), 'utf8');
  const flags = '-trimpath -buildvcs=false -ldflags "-s -w -X main.Version=';
  assert.ok(packager.includes(`"go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-s -w -X main.Version=`), 'package-release.py build command');
  assert.ok(bash.includes(`build ${flags}$VERSION"`), 'install.sh build command');
  assert.ok(powershell.includes(`'build', '-trimpath', '-buildvcs=false', '-ldflags', "-s -w -X main.Version=$targetVersion"`), 'install.ps1 build command');
  for (const [name, source] of [['install.sh', bash], ['install.ps1', powershell]]) {
    for (const setting of [/GOTOOLCHAIN\W+local/, /GOFLAGS\W+-mod=vendor/, /GOPROXY\W+off/, /GOWORK\W+off/, /GOENV\W+off/, /CGO_ENABLED\W+0/]) {
      assert.match(source, setting, `${name} lacks ${setting}`);
    }
    assert.match(source, /GO\*\|CGO_\*|\(\?i:GO\|CGO_\)/, `${name} must clear the user's Go settings`);
    assert.match(source, /--exclude\W+testdata/, `${name} must skip Go's test data`);
    assert.match(source, /definition go|Definition go/, `${name} must take the toolchain from the manifest`);
  }
});

test('both installers use one runtime per user activated through ~/.facet/current', () => {
  for (const source of [bash, powershell]) {
    assert.match(source, /runtimes/);
    assert.match(source, /current/);
    assert.match(source, /installer\.json/);
    assert.match(source, /components\.json/);
    assert.match(source, /\.facet-files\.sha256/);
  }
  assert.match(bash, /COMPONENTS=\$\{COMPONENTS:-remotion,piper\}/);
});

test('wiring is delegated to facet wire, including removal on uninstall', () => {
  for (const source of [bash, powershell]) {
    assert.match(source, /wire/);
    assert.match(source, /--remove/);
  }
});

test('installers write nothing into projects or instruction files', () => {
  for (const source of [bash, powershell]) {
    assert.doesNotMatch(source, /run-facet/);
    assert.doesNotMatch(source, /facet:managed:start/);
    assert.doesNotMatch(source, /installation\.(json|tsv)/);
    assert.doesNotMatch(source, /migrate-?legacy/i);
    assert.doesNotMatch(source, /\bstudio\b/i);
    assert.doesNotMatch(source, /--target\b/);
  }
});

test('installers point v1 users at the v1.1.0 uninstaller instead of migrating', () => {
  for (const source of [bash, powershell]) {
    assert.match(source, /v1 project integrations are separate/);
    assert.match(source, /--action uninstall/);
  }
});

test('the bash PATH change is a removable, guarded profile block that --no-path skips', () => {
  assert.match(bash, /# >>> facet path >>>/);
  assert.match(bash, /# <<< facet path <<</);
  assert.match(bash, /NO_PATH/);
  assert.match(bash, /remove_profile_block/);
});

test('public installers do not advertise downloads from the private gflow repository', () => {
  assert.doesNotMatch(manifest, /^component\tgflow\t/m);
  assert.doesNotMatch(bash, /Download gflow|releases\/download\/v.*gflow/s);
  assert.doesNotMatch(powershell, /Download gflow|releases\/download\/v.*gflow/s);
});

test('linux installer accepts current browser dependency package names and non-apt systems', () => {
  assert.match(bash, /apt-get update/);
  assert.match(bash, /apt-cache show fonts-liberation/);
  assert.match(bash, /package=\$\{package\/\/fonts-liberation\/fonts-liberation2\}/);
  assert.match(bash, /has no apt-get/);
  assert.match(bash, /browser_libraries_missing/);
});
