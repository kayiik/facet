import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { inspectMedia, decodedMedia } from './uat-media.mjs';
import { sanitizer } from './uat-sanitize.mjs';
import { facetMcpServers, mcpListTools, opencodeUserConfigFiles, parseJsonc } from './uat-wiring.mjs';

// The end-to-end journey of an installed facet. It runs INSIDE the container
// that scripts/uat.py builds from scripts/uat.Dockerfile, as that machine's one
// person, with no network, after the release's own install.sh installed facet
// in bundle mode, wired into OpenCode, with the default components. Every
// check drives the installed facet the way a person or OpenCode would; nothing
// here fabricates media, envelopes or wiring.

// The only files the journey writes to its evidence folder.
export const evidenceFiles = [
  'progress.json', 'checks.json', 'install.log', 'verify.log', 'components.json', 'browser-libraries.log',
  'wire.log', 'opencode-config.json', 'mcp-tools.json', 'opencode.log', 'doctor.log', 'render-props.json',
  'render.log', 'final.mp4', 'review-input.json', 'review.log', 'review-negative.log', 'broken-runtime.log',
];

// The components install.sh --components defaults installs.
export const defaultComponents = ['ffmpeg', 'node', 'browser', 'remotion', 'hyperframes'];

export const reviewRequest = {
  input: 'renders/final.mp4',
  profile: { width: 640, height: 360, fps: 30 },
  checks: {
    duration: { expected: 2, tolerance: 0.15 },
    video_codec: 'h264',
    pixel_format: 'yuv420p',
    audio: { required: true, codec: 'aac', sample_rate: 48000, channels: 2 },
  },
  samples: { type: 'uniform', count: 4 },
  evidence_dir: 'review/frames',
};

export const renderRequest = {
  width: 640,
  height: 360,
  fps: 30,
  cuts: [
    { id: 'title', type: 'hero_title', text: 'Facet CLI journey', subtitle: 'Deterministic local render', in_seconds: 0, out_seconds: 1 },
    { id: 'stat', type: 'stat_card', stat: '2 s', label: 'installed facet, no model', backgroundColor: '#1d4ed8', in_seconds: 1, out_seconds: 2 },
  ],
  audio_path: 'narration/tone.wav',
  output: 'renders/final.mp4',
  timeout_seconds: 300,
};

// Writes JSON through a temporary file, so a reader never sees half of it.
export function atomicJSON(file, value, redact = text => text) {
  const temporary = `${file}.writing`;
  fs.writeFileSync(temporary, redact(JSON.stringify(value, null, 2)), { mode: 0o600 });
  fs.renameSync(temporary, file);
}

const sha256 = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');

// release is the folder install.sh installed from; its build-manifest.json
// names the version.
export async function journey(evidence, { home = os.homedir(), release = '/release' } = {}) {
  fs.mkdirSync(evidence, { recursive: true });
  const { redact } = sanitizer();
  const started = Date.now();
  // Where install.sh installs facet for this person, and its link on PATH.
  const root = path.join(home, '.local', 'lib', 'facet');
  const facet = path.join(root, 'bin', 'facet');
  const link = path.join(home, '.local', 'bin', 'facet');
  let version = null;
  try { version = JSON.parse(fs.readFileSync(path.join(release, 'build-manifest.json'), 'utf8')).version; } catch { /* The layout check fails. */ }
  const report = { track: 'cli-journey', version, real_agent_acceptance: false, checks: [] };
  const state = {};
  const save = (name, text) => fs.writeFileSync(path.join(evidence, name), redact(text));
  const progress = phase => atomicJSON(path.join(evidence, 'progress.json'), {
    status: 'running', final: false, adjudication: false, phase, elapsed_ms: Date.now() - started,
    updated_at: new Date().toISOString(), checks: report.checks.map(({ name, passed }) => ({ name, passed })),
  });
  // Every program sees this home and nothing of another: OpenCode reads its
  // configuration from the XDG folders.
  const homeEnv = () => ({
    ...process.env, HOME: home, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_DATA_HOME: path.join(home, '.local', 'share'),
    XDG_STATE_HOME: path.join(home, '.local', 'state'), XDG_CACHE_HOME: path.join(home, '.cache'),
  });
  // No shell; every argument is fixed by this script.
  const run = (command, args, { cwd = home, timeout = 120000, allowFailure = false, env = homeEnv() } = {}) => {
    const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', timeout, maxBuffer: 64 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] });
    if (result.error) throw new Error(`${command} could not run: ${result.error.message}`);
    if (!allowFailure && result.status !== 0) throw new Error(`${command} ${args.join(' ')} exited ${result.status ?? result.signal}: ${(result.stderr || result.stdout).trim().slice(-2000)}`);
    return result;
  };
  const transcript = (label, result) => `$ ${label}\n${result.stdout}${result.stderr ? `\n--- stderr ---\n${result.stderr}` : ''}`;
  const tool = (args, { command = facet, ...options } = {}) => {
    const result = run(command, args, { ...options, allowFailure: true });
    let envelope = null;
    try { envelope = JSON.parse(result.stdout); } catch { /* Asserted by the caller. */ }
    return { ...result, envelope, log: transcript(`${command === link ? '~/.local/bin/facet' : 'facet'} ${args.join(' ')}`, result) };
  };
  const check = async (name, fn) => {
    progress(name);
    const checkStarted = Date.now();
    try {
      const facts = await fn();
      report.checks.push({ name, passed: true, elapsed_ms: Date.now() - checkStarted, facts });
    } catch (error) {
      report.checks.push({ name, passed: false, elapsed_ms: Date.now() - checkStarted, error: redact(error.message) });
    }
  };

  await check('installed-layout', () => {
    assert.match(version ?? '', /^\d+\.\d+\.\d+/, `no release version in ${path.join(release, 'build-manifest.json')}`);
    const target = { x64: 'linux/amd64', arm64: 'linux/arm64' }[process.arch];
    save('install.log', fs.readFileSync(path.join(home, 'uat-install.log'), 'utf8'));
    assert.ok(fs.statSync(facet).isFile(), `${facet} is missing`);
    assert.equal(fs.readlinkSync(link), facet, `${link} does not link to ${facet}`);
    assert.equal(run(facet, ['version']).stdout.trim(), `facet v${version}`);
    assert.equal(run('sh', ['-c', 'command -v facet']).stdout.trim(), link, 'facet on PATH is not the installer\'s link');
    const receipt = fs.readFileSync(path.join(root, 'install-receipt.tsv'), 'utf8').trim().split('\n').map(line => line.split('\t'));
    const field = name => receipt.find(row => row[0] === name)?.[1];
    assert.equal(field('format'), 'facet-install-v1');
    assert.equal(field('version'), version);
    assert.equal(field('target'), target);
    assert.deepEqual(receipt.filter(row => row[0] === 'product').map(row => row[1]), ['core'], 'bundle mode installs facet alone');
    const verified = run('sh', [path.join(release, 'install.sh'), '--verify'], { allowFailure: true });
    save('verify.log', transcript('install.sh --verify', verified));
    assert.equal(verified.status, 0, `install.sh --verify exited ${verified.status}`);
    for (const retired of [path.join(home, '.facet', 'current'), path.join(home, '.facet', 'runtimes')]) {
      assert.equal(fs.existsSync(retired), false, `the retired layout ${retired} exists`);
    }
    assert.equal(fs.existsSync(path.join(home, '.local', 'share', 'applications', 'facet.desktop')), false, 'bundle mode made an App entry');
    // install.sh links into ~/.local/bin and edits no shell profile.
    for (const profile of ['.bashrc', '.profile']) {
      assert.equal(fs.readFileSync(path.join(home, profile), 'utf8'), fs.readFileSync(path.join('/etc/skel', profile), 'utf8'), `${profile} changed`);
    }
    for (const profile of ['.bash_profile', '.bash_login', '.zshrc', '.zprofile']) assert.equal(fs.existsSync(path.join(home, profile)), false, `${profile} was created`);
    return { version, target, root, facet_sha256: sha256(facet) };
  });

  await check('components', () => {
    const listed = tool(['components', '--json']);
    save('components.json', listed.stdout);
    assert.equal(listed.status, 0, `facet components exited ${listed.status ?? listed.signal}`);
    const components = Object.fromEntries((listed.envelope?.result?.components ?? []).map(component => [component.id, component]));
    const dependencies = path.join(root, 'dependencies') + path.sep;
    for (const id of defaultComponents) {
      const component = components[id];
      assert.equal(component?.state, 'installed', `${id} is ${component?.state ?? 'not reported'}: ${component?.detail ?? ''}`);
      assert.ok(component.path.startsWith(dependencies), `${id} runs from ${component.path}, not from facet's dependencies`);
    }
    // The machine had none of them, so every copy is one facet downloaded.
    const elsewhere = run('sh', ['-c', 'for p in node npm ffmpeg ffprobe chromium chromium-browser google-chrome; do command -v "$p"; done'], { allowFailure: true }).stdout.trim();
    assert.equal(elsewhere, '', `programs outside facet: ${elsewhere}`);
    // Every shared library the headless browser loads is on this machine.
    const ldd = run('ldd', [components.browser.path], { allowFailure: true });
    save('browser-libraries.log', transcript(`ldd ${components.browser.path}`, ldd));
    const missing = ldd.stdout.split('\n').filter(line => line.includes('not found')).map(line => line.trim().split(/\s/)[0]);
    assert.deepEqual(missing, [], `the browser needs libraries this machine lacks: ${missing.join(', ')}`);
    // The journey's own media checks run on facet's FFmpeg and Node.
    process.env.PATH = [path.dirname(components.ffmpeg.path), path.dirname(components.node.path), process.env.PATH].join(path.delimiter);
    return Object.fromEntries(defaultComponents.map(id => [id, components[id].found || components[id].version]));
  });

  await check('sanitizer-negative-control', () => {
    const safe = sanitizer({});
    safe.add('uat-secret-test-only-123');
    const text = safe.redact('{"token":"uat-secret-test-only-123","url":"/x?token=uat-secret-test-only-123&prompt=test"}');
    assert.ok(!text.includes('uat-secret-test-only-123'));
    assert.match(text, /REDACTED/);
    return { literal_and_query_token_removed: true };
  });

  await check('strict-media-probe-negative-controls', () => {
    const dir = fs.mkdtempSync(path.join(home, 'uat-probes-'));
    const valid = path.join(dir, 'valid.mp4');
    run('ffmpeg', ['-v', 'error', '-y', '-f', 'lavfi', '-i', 'color=c=blue:s=320x180:r=24:d=1', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=1', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', valid]);
    const garbage = path.join(dir, 'garbage.mp4');
    fs.writeFileSync(garbage, Buffer.alloc(4096, 65));
    const truncated = path.join(dir, 'truncated.mp4');
    fs.writeFileSync(truncated, fs.readFileSync(valid).subarray(0, 512));
    const silent = path.join(dir, 'silent.mp4');
    run('ffmpeg', ['-v', 'error', '-y', '-i', valid, '-an', '-c:v', 'copy', silent]);
    const wrong = path.join(dir, 'wrong.mp4');
    run('ffmpeg', ['-v', 'error', '-y', '-i', valid, '-c:v', 'mpeg4', '-c:a', 'aac', wrong]);
    const facts = inspectMedia(valid);
    for (const file of [path.join(dir, 'missing.mp4'), garbage, truncated, silent, wrong]) assert.throws(() => inspectMedia(file), path.basename(file));
    assert.throws(() => inspectMedia(valid, { minDuration: 5 }));
    assert.throws(() => inspectMedia(valid, { maxDuration: 0.5 }));
    for (const file of [valid, garbage, truncated, silent, wrong]) {
      const accepted = run('bash', [path.join(path.dirname(fileURLToPath(import.meta.url)), 'probe-artifact.sh'), file], { allowFailure: true }).status === 0;
      assert.equal(accepted, file === valid, path.basename(file));
    }
    return { valid_sha256: facts.sha256, rejected: ['missing', 'garbage', 'truncated', 'missing-audio', 'wrong-codec', 'short-duration', 'long-duration'] };
  });

  await check('wire-opencode-user-scope', async () => {
    // bundle mode wired OpenCode during the installation. Wiring again, once
    // through the link on PATH, changes nothing.
    const recorded = () => JSON.parse(fs.readFileSync(path.join(home, '.facet', 'wiring.json'), 'utf8')).wirings
      ?.find(wiring => wiring.cli === 'opencode' && wiring.scope === 'user');
    assert.ok(recorded(), 'the installer did not wire OpenCode');
    const scratch = fs.mkdtempSync(path.join(home, 'uat-wire-cwd-'));
    const args = ['wire', 'opencode', '--scope', 'user'];
    const wired = [tool(args, { cwd: scratch }), tool(args, { cwd: scratch, command: link })];
    save('wire.log', wired.map(result => result.log).join('\n\n'));
    for (const result of wired) assert.equal(result.status, 0, `facet wire exited ${result.status ?? result.signal}`);
    assert.deepEqual(fs.readdirSync(scratch), [], 'user-scope wiring wrote into the working directory');
    // The guide, the producer agent and the role agents, as recorded.
    const record = recorded();
    for (const file of record.files) assert.ok(fs.existsSync(file.path), `${file.path} is missing`);
    const files = record.files.map(file => path.relative(record.root, file.path).split(path.sep).join('/')).sort();
    for (const wanted of ['skills/facet/SKILL.md', 'agents/facet.md', 'agents/facet-critic.md', 'agents/facet-researcher.md', 'agents/facet-reference-analyst.md']) {
      assert.ok(files.includes(wanted), `the wiring lacks ${wanted}`);
    }
    const servers = opencodeUserConfigFiles(home, homeEnv()).flatMap(file => facetMcpServers(parseJsonc(fs.readFileSync(file, 'utf8'))).map(entry => ({ file, ...entry })));
    assert.equal(servers.length, 1, `expected exactly one facet MCP server after wiring three times, found ${servers.length}`);
    const [{ file, name, server }] = servers;
    save('opencode-config.json', fs.readFileSync(file, 'utf8'));
    assert.equal(server.type, 'local', 'facet must be wired as a local (stdio) MCP server');
    assert.notEqual(server.enabled, false, 'the wired facet MCP server is disabled');
    // facet wire registers the facet it runs, its link resolved: the one in
    // the programs folder, which an upgrade replaces in place.
    assert.equal(server.command[0], facet, 'OpenCode would run another facet than the installed one');
    // Speak MCP to exactly the command OpenCode will start.
    const listed = await mcpListTools(server.command[0], server.command.slice(1), { cwd: scratch, env: { ...homeEnv(), ...(server.environment ?? {}) }, timeoutMs: 60000 });
    fs.writeFileSync(path.join(evidence, 'mcp-tools.json'), JSON.stringify(listed, null, 2));
    for (const wanted of ['video_compose', 'output_review']) {
      assert.ok(listed.tools.some(tool => tool === wanted || String(tool).endsWith(`_${wanted}`)), `facet mcp does not offer ${wanted}`);
    }
    state.wired = true;
    return { config: file, server: name, command: server.command, files, server_info: listed.serverInfo, protocol_version: listed.protocolVersion, tools: listed.tools.length };
  });

  await check('opencode-loads-facet', () => {
    assert.ok(state.wired, 'facet is not wired into OpenCode');
    // OpenCode itself, offline: it must accept what facet wire wrote, load the
    // facet agent and skill, and start the facet MCP server.
    const project = fs.mkdtempSync(path.join(home, 'uat-opencode-'));
    const opencode = (...args) => {
      const result = run('opencode', args, { cwd: project, timeout: 90000, allowFailure: true });
      return { ...result, text: `${result.stdout}\n${result.stderr}`, log: transcript(`opencode ${args.join(' ')}`, result) };
    };
    const results = {
      '--version': opencode('--version'), 'debug config': opencode('debug', 'config'), 'debug agent facet': opencode('debug', 'agent', 'facet'),
      'debug skill': opencode('debug', 'skill'), 'mcp list': opencode('mcp', 'list'),
    };
    save('opencode.log', Object.values(results).map(result => result.log).join('\n\n'));
    for (const [what, result] of Object.entries(results)) assert.equal(result.status, 0, `opencode ${what} exited ${result.status ?? result.signal}`);
    assert.match(results['debug config'].text, /"facet"/, 'OpenCode\'s resolved configuration has no facet server');
    assert.match(results['debug agent facet'].text, /primary/, 'OpenCode does not load facet as a primary agent');
    assert.match(results['debug skill'].text, /\bfacet\b/, 'OpenCode does not list the facet skill');
    assert.match(results['mcp list'].text, /facet[^\n]*connected/i, 'OpenCode does not connect to the facet MCP server');
    return { opencode: results['--version'].stdout.trim() };
  });

  await check('facet-doctor', () => {
    const result = run(facet, ['doctor']);
    save('doctor.log', `${result.stdout}${result.stderr}`);
    // The runtime is the programs folder, and the receipt there names this release.
    const line = result.stdout.split('\n').find(text => /^\s*\S+\s+Runtime\s+:/.test(text))?.trim() ?? '';
    assert.ok(line.endsWith(`: ${root} (installed: Facet ${version}, core)`), `facet doctor reports ${line || 'no runtime'}`);
    // Recorded for the reader; the render below is the proof the composer works.
    const remotion = result.stdout.split('\n').find(text => /remotion/i.test(text))?.trim() ?? null;
    return { runtime: line, remotion };
  });

  const project = fs.mkdtempSync(path.join(home, 'uat-project-'));
  await check('deterministic-render', () => {
    fs.mkdirSync(path.join(project, 'narration'));
    fs.mkdirSync(path.join(project, 'artifacts'));
    run('ffmpeg', ['-v', 'error', '-y', '-f', 'lavfi', '-i', 'sine=frequency=523:sample_rate=48000:duration=2', '-ac', '2', path.join(project, 'narration', 'tone.wav')]);
    const input = path.join(project, 'artifacts', 'props.json');
    fs.writeFileSync(input, JSON.stringify(renderRequest, null, 2));
    fs.copyFileSync(input, path.join(evidence, 'render-props.json'));
    const final = path.join(project, 'renders', 'final.mp4');
    assert.equal(fs.existsSync(final), false, 'fresh project already contains output');
    const rendered = tool(['tools', 'run', 'video_compose', '--input', 'artifacts/props.json'], { cwd: project, timeout: 330000 });
    save('render.log', rendered.log);
    assert.equal(rendered.status, 0, `video_compose exited ${rendered.status ?? rendered.signal}`);
    const { envelope } = rendered;
    assert.equal(envelope?.ok, true, 'video_compose did not return a successful envelope');
    assert.equal(envelope.tool, 'video_compose');
    assert.equal(envelope.operation, 'run');
    assert.equal(path.resolve(project, envelope.result?.output ?? ''), final, 'video_compose reported another output');
    const facts = inspectMedia(final, { minDuration: 1.9, maxDuration: 2.2 });
    const video = facts.streams.find(stream => stream.codec_type === 'video');
    const audio = facts.streams.find(stream => stream.codec_type === 'audio');
    assert.equal(video.width, 640);
    assert.equal(video.height, 360);
    assert.equal(video.avg_frame_rate, '30/1');
    assert.equal(Number(audio.sample_rate), 48000);
    assert.equal(audio.channels, 2);
    assert.equal(envelope.result.output_facts?.sha256, facts.sha256, 'reported output_facts.sha256 is not the delivered file');
    const decoded = decodedMedia(final, { sampleSeconds: 1 });
    fs.copyFileSync(final, path.join(evidence, 'final.mp4'));
    state.render = { sha256: facts.sha256 };
    return { project_outside_source: true, sha256: facts.sha256, duration: Number(facts.format.duration), decoded, warnings: envelope.warnings };
  });

  await check('output-review', () => {
    assert.ok(state.render, 'no rendered output to review');
    const review = (request, name) => {
      const input = path.join(project, 'artifacts', `${name}.json`);
      fs.writeFileSync(input, JSON.stringify(request, null, 2));
      const result = tool(['tools', 'run', 'output_review', '--input', path.relative(project, input)], { cwd: project, timeout: 180000 });
      save(`${name}.log`, result.log);
      assert.equal(result.status, 0, `output_review exited ${result.status ?? result.signal}`);
      assert.equal(result.envelope?.ok, true, 'output_review did not return a successful envelope');
      return result.envelope.result;
    };
    fs.writeFileSync(path.join(evidence, 'review-input.json'), JSON.stringify(reviewRequest, null, 2));
    const result = review(reviewRequest, 'review');
    const gates = Object.fromEntries(result.gates.map(gate => [gate.name, gate.status]));
    assert.equal(result.review_status, 'pass', `review ${result.review_status}: ${JSON.stringify(gates)}`);
    for (const name of ['profile', 'duration', 'video_codec', 'pixel_format', 'audio', 'content']) assert.equal(gates[name], 'pass', `${name} gate is ${gates[name]}`);
    assert.ok(result.gates.every(gate => gate.status === 'pass'), `every gate must verify a stated expectation: ${JSON.stringify(gates)}`);
    assert.equal(result.output_facts?.sha256, state.render.sha256, 'output_review inspected other bytes');
    // Negative control: a wrong stated profile must fail the review.
    const negative = review({ ...reviewRequest, profile: { width: 1280, height: 720, fps: 30 }, evidence_dir: 'review/negative-frames' }, 'review-negative');
    assert.equal(negative.review_status, 'fail', 'output_review passed a wrong expectation');
    assert.equal(negative.gates.find(gate => gate.name === 'profile')?.status, 'fail');
    return { review_status: result.review_status, gates, negative_review_status: negative.review_status };
  });

  await check('broken-runtime-pin-rejected', () => {
    const broken = fs.mkdtempSync(path.join(home, 'uat-broken-runtime-'));
    fs.mkdirSync(path.join(broken, 'artifacts'));
    const output = path.join(broken, 'renders', 'must-not-exist.mp4');
    fs.writeFileSync(path.join(broken, 'artifacts', 'props.json'), JSON.stringify({ ...renderRequest, audio_path: undefined, output: 'renders/must-not-exist.mp4' }));
    // The one explicit composer override, pointing nowhere: the render must
    // fail rather than fall back to another composer.
    const env = { ...homeEnv(), FACET_REMOTION_COMPOSER: '/nonexistent-uat-runtime' };
    const result = tool(['tools', 'run', 'video_compose', '--input', 'artifacts/props.json'], { cwd: broken, timeout: 60000, env });
    save('broken-runtime.log', result.log);
    assert.notEqual(result.status, 0, 'a broken runtime pin must fail');
    assert.equal(result.envelope?.ok, false);
    assert.equal(result.envelope.error?.code, 'dependency_missing');
    assert.equal(fs.existsSync(output), false, 'a failed render published output');
    return { exit_code: result.status, error_code: result.envelope.error.code };
  });

  const passed = report.checks.length > 0 && report.checks.every(entry => entry.passed);
  atomicJSON(path.join(evidence, 'checks.json'), { ...report, status: 'complete', final: true, passed, elapsed_ms: Date.now() - started }, redact);
  atomicJSON(path.join(evidence, 'progress.json'), { status: 'complete', final: true, adjudication: false, phase: 'complete', elapsed_ms: Date.now() - started, updated_at: new Date().toISOString(), checks: report.checks.map(({ name, passed: ok }) => ({ name, passed: ok })) });
  return { passed, report };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { passed, report } = await journey(path.resolve(process.argv[2] || path.join(os.homedir(), 'uat-evidence')));
  const checks = report.checks.map(({ name, passed: ok, error }) => (ok ? { name, passed: ok } : { name, passed: ok, error }));
  console.log(JSON.stringify({ track: report.track, version: report.version, passed, checks }, null, 2));
  if (!passed) process.exitCode = 1;
}
