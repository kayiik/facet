import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { runWithCheckpoints } from './uat-checkpoint.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const usage = 'Usage: node scripts/uat-run.mjs <external-evidence-root> [--certify]';

export function configureRun(args, inherited = process.env) {
  const certify = args.includes('--certify');
  const positional = args.filter(arg => arg !== '--certify');
  if (positional.length !== 1 || positional[0].startsWith('--') || args.filter(arg => arg === '--certify').length > 1) throw new Error(usage);
  const evidence = path.resolve(positional[0]);
  const relative = path.relative(root, evidence);
  if (!relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)) throw new Error('Supply an evidence root outside the source repository');
  // Resolve existing ancestors as well, so an external symlink cannot point into source.
  let ancestor = evidence;
  while (!fs.existsSync(ancestor)) ancestor = path.dirname(ancestor);
  const realRelative = path.relative(fs.realpathSync(root), fs.realpathSync(ancestor));
  if (!realRelative.startsWith(`..${path.sep}`) && !path.isAbsolute(realRelative)) throw new Error('Supply an evidence root outside the source repository');
  const env = { ...inherited };
  // The journey needs no credentials or model configuration. Keep host-side
  // probes from inheriting them, and from user-wide Compose or UAT overrides.
  for (const key of Object.keys(env)) {
    if (/TOKEN|SECRET|PASSWORD|API_KEY|ACCESS_KEY|AUTHORIZATION/i.test(key) || /^(OPENCODE_|COMPOSE_|UAT_)/.test(key)) delete env[key];
  }
  env.UAT_PORT = '31000';
  env.UAT_APP_URL = 'http://127.0.0.1:31000';
  env.UAT_CERTIFY = certify ? '1' : '0';
  return { evidence, env, certify };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { evidence, env, certify } = configureRun(process.argv.slice(2));
  const runId = `facet-uat-${Date.now()}`;
  fs.mkdirSync(evidence, { recursive: true });
  console.log(`UAT mode: ${certify ? 'CERTIFICATION (clean source required)' : 'DEVELOPMENT (--allow-dirty)'}`);
  console.log('UAT journey: release archive -> install.sh --components remotion --no-path -> facet wire opencode --scope user -> facet doctor -> video_compose -> output_review. Sealed network, no model calls.');
  const started = Date.now();
  console.log('UAT budgets: image build with installation <=30m; journey <=15m; runner 50m; external supervisor >=55m.');
  const result = await runWithCheckpoints(process.execPath, [path.join(root, 'node_modules/@xibodev/release-harness-core/bin/release-harness.js'), 'run-local', ...(certify ? [] : ['--allow-dirty']), '--run-id', runId, '--evidence-dir', evidence, '--port-offset', '0'], {
    cwd: root, env, stdio: 'inherit', timeout: 3000000,
    checkpoint: () => console.log(JSON.stringify({ run_id: runId, marker: 'launcher-progress-not-adjudication', elapsed_ms: Date.now() - started, updated_at: new Date().toISOString() })),
  });
  console.log(`Run evidence: ${path.join(evidence, 'runs', runId)}`);
  // Core 1.2.0 can skip teardown after a partial up failure. Clean only this run.
  const cleanup = spawnSync('docker', ['compose', '-p', `rh-${runId}`, '-f', path.join(root, 'docker-compose.test.yml'), 'down', '-v', '--remove-orphans'], { cwd: root, env: { ...env, RUN_ID: runId }, stdio: 'inherit', timeout: 60000 });
  if (result.error) console.error('Harness process failed or exceeded its overall timeout');
  process.exitCode = result.exit_code ?? 3;
  if (cleanup.status !== 0 && process.exitCode === 0) process.exitCode = 3;
}
