const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const launcher = fs.readFileSync(path.join(__dirname, 'facet-cli.js'), 'utf8');
const exit = new Error('process.exit');

// A synthetic installation layout per platform; nothing touches the real disk.
function layout(platform) {
  const paths = platform === 'win32' ? path.win32 : path.posix;
  const home = platform === 'win32' ? 'C:\\Users\\Test User' : '/home/test';
  const exe = platform === 'win32' ? '.exe' : '';
  const npmPrefix = platform === 'win32' ? paths.join(home, 'AppData', 'Roaming', 'npm') : '/usr/local/bin';
  const packageBin = platform === 'win32'
    ? paths.join(npmPrefix, 'node_modules', '@kayiik', 'facet', 'bin')
    : '/usr/local/lib/node_modules/@kayiik/facet/bin';
  return {
    paths, home, exe, npmPrefix, packageBin,
    installed: platform === 'win32'
      ? paths.join(home, 'AppData', 'Local', 'Programs', 'Facet', 'bin', 'facet.exe')
      : paths.join(home, '.local', 'lib', 'facet', 'bin', 'facet'),
    onPath: platform === 'win32' ? 'D:\\Tools\\Facet\\bin' : '/opt/homebrew/bin',
    explicit: paths.join(home, 'custom install', `facet${exe}`),
    // npm's own command for this launcher: a link (macOS, Linux) or shims (Windows).
    npmCommands: platform === 'win32'
      ? ['facet', 'facet.cmd', 'facet.ps1'].map(name => paths.join(npmPrefix, name))
      : [paths.join(npmPrefix, 'facet')],
    retired: [
      paths.join(packageBin, `facet${exe}`),
      paths.join(home, '.facet', 'current', 'bin', `facet${exe}`),
      paths.join(home, '.facet', 'bin', `facet${exe}`),
    ],
  };
}

// Runs the launcher with mocked modules and records what it would spawn.
// files maps a path to 'program' or 'script', its first two bytes.
function launch({ platform, arch = 'x64', files = {}, env = {}, argv = ['version'] }) {
  const { paths, home, packageBin, npmPrefix } = layout(platform);
  const calls = [];
  const errors = [];
  const codes = [];
  const opened = [];
  const missing = () => Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
  const context = {
    __dirname: packageBin,
    console: { error: message => errors.push(String(message)) },
    process: {
      argv: ['node', paths.join(packageBin, 'facet-cli.js'), ...argv],
      env: { PATH: npmPrefix, ...env },
      pid: 1,
      exit(code) { codes.push(code); throw exit; },
      kill() { throw new Error('unexpected kill'); },
    },
    require(module) {
      if (module === 'path') return paths;
      if (module === 'os') return { platform: () => platform, arch: () => arch, homedir: () => home };
      if (module === 'fs') {
        return {
          existsSync: candidate => candidate in files,
          statSync(candidate) { if (!(candidate in files)) throw missing(); return { isFile: () => true }; },
          openSync(candidate) { if (!(candidate in files)) throw missing(); opened.push(candidate); return opened.length; },
          readSync(fd, buffer) {
            const head = files[opened[fd - 1]] === 'script' ? [0x23, 0x21] : [0x4d, 0x5a];
            buffer[0] = head[0];
            buffer[1] = head[1];
            return 2;
          },
          closeSync() {},
        };
      }
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
  assert.match(message, /Facet is not installed/);
  assert.ok(message.includes('curl -fsSL https://kayiik.github.io/facet/install.sh | sh'), message);
  assert.ok(message.includes('irm https://kayiik.github.io/facet/install.ps1 | iex'), message);
}

const only = (...names) => Object.fromEntries(names.map(name => [name, 'program']));

for (const platform of ['linux', 'darwin', 'win32']) {
  const at = layout(platform);
  const pathWith = (...dirs) => dirs.join(at.paths.delimiter);
  const onPath = at.paths.join(at.onPath, `facet${at.exe}`);
  const scripts = Object.fromEntries(at.npmCommands.map(name => [name, 'script']));

  test(`${platform}: fails with installer guidance when Facet is not installed`, () => {
    assertMissing(launch({ platform, files: scripts }));
  });

  for (const arch of ['x64', 'arm64']) {
    test(`${platform}/${arch}: runs the facet the installer installed`, () => {
      assertLaunched(launch({ platform, arch, files: { ...scripts, ...only(at.installed) } }), at.installed);
    });
  }

  test(`${platform}: runs a facet on PATH, but never npm's command for this launcher`, () => {
    const env = { PATH: pathWith(at.npmPrefix, at.onPath) };
    assertLaunched(launch({ platform, env, files: { ...scripts, ...only(onPath) } }), onPath);
    assertMissing(launch({ platform, env, files: scripts }));
  });

  test(`${platform}: the installer's facet comes before one on PATH`, () => {
    const env = { PATH: pathWith(at.onPath) };
    assertLaunched(launch({ platform, env, files: only(onPath, at.installed) }), at.installed);
  });

  test(`${platform}: an existing FACET_BIN wins`, () => {
    const files = only(at.explicit, at.installed);
    assertLaunched(launch({ platform, env: { FACET_BIN: at.explicit }, files }), at.explicit);
  });

  test(`${platform}: a missing FACET_BIN falls through, then fails safely`, () => {
    const env = { FACET_BIN: at.explicit };
    assertLaunched(launch({ platform, env, files: only(at.installed) }), at.installed);
    assertMissing(launch({ platform, env, files: scripts }));
  });

  test(`${platform}: never runs a binary beside the launcher or in a retired location`, () => {
    // The npm package ships no binary; one placed beside the launcher is not a
    // facet the installer manages, nor is one in Facet's old runtime folders.
    assertMissing(launch({ platform, files: { ...scripts, ...only(...at.retired) } }));
  });

  if (platform === 'win32') {
    test('win32: LOCALAPPDATA moves the installer\'s folder', () => {
      const moved = 'E:\\Local';
      const installed = at.paths.join(moved, 'Programs', 'Facet', 'bin', 'facet.exe');
      const env = { LOCALAPPDATA: moved };
      assertLaunched(launch({ platform, env, files: only(installed, at.installed) }), installed);
    });
  }

  test(`${platform}: forwards arguments verbatim`, () => {
    const argv = ['tools', 'run', 'video_compose', '--input', 'my props.json'];
    assertLaunched(launch({ platform, files: only(at.installed), argv }), at.installed, argv);
  });
}

test('the npm package exposes only the facet launcher', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  assert.deepEqual(manifest.bin, { facet: 'bin/facet-cli.js' });
  assert.deepEqual(fs.readdirSync(__dirname).filter(name => name.endsWith('-cli.js')), ['facet-cli.js']);
  assert.deepEqual(manifest.files, ['bin/facet-cli.js', 'LICENSE', 'THIRD_PARTY_NOTICES.md']);
});
