const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const launcher = fs.readFileSync(path.join(__dirname, 'facet-cli.js'), 'utf8');
const exit = new Error('process.exit');
const goos = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
const goarch = { x64: 'amd64', arm64: 'arm64' };

// A synthetic installation layout per platform; nothing touches the real disk.
function layout(platform) {
  const paths = platform === 'win32' ? path.win32 : path.posix;
  const home = platform === 'win32' ? 'C:\\Users\\Test User' : '/home/test';
  const exe = platform === 'win32' ? '.exe' : '';
  const packageBin = paths.join(home, 'node_modules', '@kayiik', 'facet', 'bin');
  return {
    paths, home, exe, packageBin,
    userRuntime: paths.join(home, '.facet', 'current', 'bin', `facet${exe}`),
    packaged: arch => paths.join(packageBin, `facet-${goos[platform]}-${goarch[arch]}${exe}`),
    explicit: paths.join(home, 'custom install', `facet${exe}`),
    retired: [
      paths.join(packageBin, `facet${exe}`),
      paths.join(home, '.facet', 'bin', `facet${exe}`),
      paths.join(home, 'AppData', 'Local', 'Programs', 'Facet', 'bin', `facet${exe}`),
    ],
  };
}

// Runs the launcher with mocked modules and records what it would spawn.
function launch({ platform, arch = 'x64', files = [], env = {}, argv = ['version'] }) {
  const { paths, home, packageBin } = layout(platform);
  const calls = [];
  const errors = [];
  const codes = [];
  const context = {
    __dirname: packageBin,
    console: { error: message => errors.push(String(message)) },
    process: {
      argv: ['node', paths.join(packageBin, 'facet-cli.js'), ...argv],
      env,
      pid: 1,
      exit(code) { codes.push(code); throw exit; },
      kill() { throw new Error('unexpected kill'); },
    },
    require(module) {
      if (module === 'path') return paths;
      if (module === 'os') return { platform: () => platform, arch: () => arch, homedir: () => home };
      if (module === 'fs') return { existsSync: candidate => files.includes(candidate) };
      if (module === 'child_process') return { spawn(...args) { calls.push(args); return { on() {} }; } };
      throw new Error(`Unexpected module ${module}`);
    },
  };
  try { vm.runInNewContext(launcher, context); } catch (error) { if (error !== exit) throw error; }
  return { calls, errors, codes };
}

function assertLaunched(result, expected, argv = ['version']) {
  assert.deepEqual(result.errors, []);
  assert.equal(result.calls.length, 1);
  assert.equal(result.calls[0][0], expected);
  assert.deepEqual([...result.calls[0][1]], argv);
  // Spread: objects created inside the vm context have another realm's prototype.
  assert.deepEqual({ ...result.calls[0][2] }, { stdio: 'inherit' });
}

function assertMissing(result) {
  assert.equal(result.calls.length, 0, 'must not spawn anything, including itself through PATH');
  assert.deepEqual(result.codes, [1]);
  const message = result.errors.join('\n');
  assert.match(message, /Facet native binary not found/);
  assert.ok(message.includes('curl -fsSL https://kayiik.github.io/facet/install.sh | sh'), message);
  assert.ok(message.includes('irm https://kayiik.github.io/facet/install.ps1 | iex'), message);
}

for (const platform of ['linux', 'darwin', 'win32']) {
  const at = layout(platform);
  for (const arch of ['x64', 'arm64']) {
    const label = `${platform}/${arch}`;
    const other = arch === 'x64' ? 'arm64' : 'x64';

    test(`${label}: fails with installer guidance when no binary exists`, () => {
      assertMissing(launch({ platform, arch }));
    });

    test(`${label}: uses the active user runtime`, () => {
      assertLaunched(launch({ platform, arch, files: [at.userRuntime] }), at.userRuntime);
    });

    test(`${label}: FACET_HOME moves the active runtime it uses`, () => {
      const moved = at.paths.join(at.home, 'App Facet');
      const runtime = at.paths.join(moved, 'current', 'bin', `facet${at.exe}`);
      const env = { FACET_HOME: moved };
      assertLaunched(launch({ platform, arch, env, files: [runtime, at.userRuntime] }), runtime);
      assertMissing(launch({ platform, arch, env, files: [at.userRuntime] }));
    });

    test(`${label}: an existing FACET_BIN wins`, () => {
      const files = [at.explicit, at.userRuntime];
      assertLaunched(launch({ platform, arch, env: { FACET_BIN: at.explicit }, files }), at.explicit);
    });

    test(`${label}: a missing FACET_BIN falls through, then fails safely`, () => {
      const env = { FACET_BIN: at.explicit };
      assertLaunched(launch({ platform, arch, env, files: [at.userRuntime] }), at.userRuntime);
      assertMissing(launch({ platform, arch, env }));
    });

    test(`${label}: never runs a binary beside the launcher or in a retired location`, () => {
      // The npm package ships no binary; one placed beside the launcher is
      // not a Facet runtime the installer manages.
      const nodeNamed = at.paths.join(at.packageBin, `facet-${platform}-${arch}${at.exe}`);
      assertMissing(launch({ platform, arch, files: [at.packaged(arch), at.packaged(other), nodeNamed, ...at.retired] }));
    });
  }

  test(`${platform}: forwards arguments verbatim`, () => {
    const argv = ['tools', 'run', 'video_compose', '--input', 'my props.json'];
    assertLaunched(launch({ platform, files: [at.userRuntime], argv }), at.userRuntime, argv);
  });
}

test('the npm package exposes only the facet launcher', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  assert.deepEqual(manifest.bin, { facet: 'bin/facet-cli.js' });
  assert.deepEqual(fs.readdirSync(__dirname).filter(name => name.endsWith('-cli.js')), ['facet-cli.js']);
  assert.deepEqual(manifest.files, ['bin/facet-cli.js', 'LICENSE', 'THIRD_PARTY_NOTICES.md']);
});
