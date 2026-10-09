import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { atomicJSON } from './uat-checkpoint.mjs';
import { inspectMedia, decodedMedia } from './uat-media.mjs';
import { sanitizer } from './uat-sanitize.mjs';
import { facetMcpServers, mcpListTools, opencodeUserConfigFiles, parseJsonc } from './uat-wiring.mjs';

// The Facet 2.0 CLI journey. Runs INSIDE the UAT container as the unprivileged
// user, after the image build installed the release archive with the product
// installer. Every check drives the installed `facet` the way a user or an
// agentic CLI would; nothing here fabricates media, envelopes or wiring.
export const evidenceFiles = [
  'progress.json', 'checks.json', 'install-build.log', 'wire.log', 'opencode-config.json', 'mcp-tools.json',
  'doctor.log', 'render-props.json', 'render.log', 'final.mp4', 'review-input.json', 'review.log',
  'review-negative.log', 'broken-runtime.log',
];

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
    { id: 'stat', type: 'stat_card', stat: '2 s', label: 'installed runtime, no model', backgroundColor: '#1d4ed8', in_seconds: 1, out_seconds: 2 },
  ],
  audio_path: 'narration/tone.wav',
  output: 'renders/final.mp4',
  timeout_seconds: 300,
};

const sha256 = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');

export async function journey(evidence, { home = os.homedir(), version = process.env.FACET_UAT_VERSION } = {}) {
  fs.mkdirSync(evidence, { recursive: true });
  const { redact } = sanitizer();
  const started = Date.now();
  const goarch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  const current = path.join(home, '.facet', 'current');
  const facet = path.join(current, 'bin', 'facet');
  const report = { track: 'cli-journey', version, real_agent_acceptance: false, checks: [] };
  const state = {};
  const save = (name, text) => fs.writeFileSync(path.join(evidence, name), redact(text));
  const progress = phase => atomicJSON(path.join(evidence, 'progress.json'), {
    status: 'running', final: false, adjudication: false, phase, elapsed_ms: Date.now() - started,
    updated_at: new Date().toISOString(), checks: report.checks.map(({ name, passed }) => ({ name, passed })),
  });
  // No shell; every argument is fixed by this script.
  const run = (command, args, { cwd = home, timeout = 120000, allowFailure = false, env = process.env } = {}) => {
    const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', timeout, maxBuffer: 64 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] });
    if (result.error) throw new Error(`${command} could not run: ${result.error.message}`);
    if (!allowFailure && result.status !== 0) throw new Error(`${command} ${args.join(' ')} exited ${result.status ?? result.signal}: ${(result.stderr || result.stdout).trim().slice(-2000)}`);
    return result;
  };
  const tool = (args, options) => {
    const result = run(facet, args, { ...options, allowFailure: true });
    let envelope = null;
    try { envelope = JSON.parse(result.stdout); } catch { /* Asserted by the caller. */ }
    return { ...result, envelope, log: `$ facet ${args.join(' ')}\n${result.stdout}${result.stderr ? `\n--- stderr ---\n${result.stderr}` : ''}` };
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

  await check('installed-runtime-layout', () => {
    assert.match(version ?? '', /^\d+\.\d+\.\d+/, 'FACET_UAT_VERSION must name the installed release');
    assert.ok(goarch, `unsupported architecture ${process.arch}`);
    save('install-build.log', fs.readFileSync(path.join(home, 'uat-install-build.log'), 'utf8'));
    const runtime = path.join(home, '.facet', 'runtimes', `${version}-linux-${goarch}`);
    for (const file of [path.join(runtime, 'bin', 'facet'), facet]) assert.ok(fs.statSync(file).isFile(), `${file} is missing`);
    assert.equal(sha256(facet), sha256(path.join(runtime, 'bin', 'facet')), '~/.facet/current is not the installed release');
    assert.equal(run(facet, ['version']).stdout.trim(), `facet v${version}`);
    assert.equal(run('sh', ['-c', 'command -v facet']).stdout.trim(), facet, 'facet on PATH must be the active runtime');
    for (const retired of ['bin', 'bundle']) assert.equal(fs.existsSync(path.join(home, '.facet', retired)), false, `retired ~/.facet/${retired} layout exists`);
    // --no-path: login and shell profiles are exactly the account skeleton.
    for (const profile of ['.bashrc', '.profile']) {
      assert.equal(fs.readFileSync(path.join(home, profile), 'utf8'), fs.readFileSync(path.join('/etc/skel', profile), 'utf8'), `${profile} changed despite --no-path`);
    }
    for (const profile of ['.bash_profile', '.bash_login', '.zshrc', '.zprofile']) assert.equal(fs.existsSync(path.join(home, profile)), false, `${profile} created despite --no-path`);
    return { runtime, active_runtime: fs.realpathSync(current), current_is_link: fs.lstatSync(current).isSymbolicLink(), facet_sha256: sha256(facet) };
  });

  await check('toolchain', () => {
    const versions = {};
    for (const [command, args] of [['opencode', ['--version']], ['node', ['--version']], ['ffmpeg', ['-version']], ['ffprobe', ['-version']], ['chromium', ['--version']]]) {
      versions[command] = run(command, args).stdout.split('\n')[0].trim();
    }
    assert.match(versions.opencode, /1\.18\.29/);
    return versions;
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
    const scratch = fs.mkdtempSync(path.join(home, 'uat-wire-cwd-'));
    const wired = [tool(['wire', 'opencode', '--scope', 'user'], { cwd: scratch }), tool(['wire', 'opencode', '--scope', 'user'], { cwd: scratch })];
    save('wire.log', wired.map(result => result.log).join('\n\n'));
    for (const result of wired) assert.equal(result.status, 0, `facet wire exited ${result.status ?? result.signal}`);
    assert.deepEqual(fs.readdirSync(scratch), [], 'user-scope wiring wrote into the working directory');
    const files = opencodeUserConfigFiles(home);
    assert.ok(files.length > 0, 'no OpenCode user configuration was written');
    const servers = files.flatMap(file => facetMcpServers(parseJsonc(fs.readFileSync(file, 'utf8'))).map(entry => ({ file, ...entry })));
    assert.equal(servers.length, 1, `expected exactly one facet MCP server after wiring twice, found ${servers.length}`);
    const [{ file, name, server }] = servers;
    save('opencode-config.json', fs.readFileSync(file, 'utf8'));
    assert.equal(server.type, 'local', 'facet must be wired as a local (stdio) MCP server');
    assert.notEqual(server.enabled, false, 'the wired facet MCP server is disabled');
    const executable = path.isAbsolute(server.command[0]) ? server.command[0] : run('sh', ['-c', 'command -v "$1"', 'sh', server.command[0]]).stdout.trim();
    assert.equal(sha256(executable), sha256(facet), 'the wired executable is not the installed facet');
    // Speak MCP to exactly the command OpenCode will start.
    const listed = await mcpListTools(executable, server.command.slice(1), { cwd: scratch, env: { ...process.env, ...(server.environment ?? {}) }, timeoutMs: 60000 });
    fs.writeFileSync(path.join(evidence, 'mcp-tools.json'), JSON.stringify(listed, null, 2));
    for (const wanted of ['video_compose', 'output_review']) {
      assert.ok(listed.tools.some(name => name === wanted || String(name).endsWith(`_${wanted}`)), `facet mcp does not offer ${wanted}`);
    }
    // Recorded, not asserted: a command under ~/.facet/runtimes/<version> would
    // break when an update retires that runtime; ~/.facet/current would not.
    const versioned = path.resolve(executable).startsWith(path.join(home, '.facet', 'runtimes') + path.sep);
    return { config: file, server: name, command: server.command, wired_executable: executable, survives_runtime_updates: !versioned, server_info: listed.serverInfo, protocol_version: listed.protocolVersion, tools: listed.tools.length };
  });

  await check('facet-doctor', () => {
    const result = run(facet, ['doctor']);
    save('doctor.log', `${result.stdout}${result.stderr}`);
    assert.ok(result.stdout.trim(), 'facet doctor printed no report');
    // Recorded for the reader; the render below is the proof the composer works.
    const remotion = result.stdout.split('\n').find(line => /remotion/i.test(line))?.trim() ?? null;
    return { exit_code: result.status, remotion };
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
    const env = { ...process.env, FACET_REMOTION_COMPOSER: '/nonexistent-uat-runtime' };
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
  const { passed, report } = await journey(path.resolve(process.argv[2] || '/home/facet/uat-evidence/journey'));
  console.log(JSON.stringify({ track: report.track, passed, checks: report.checks.map(({ name, passed: ok }) => ({ name, passed: ok })) }));
  if (!passed) process.exitCode = 1;
}
