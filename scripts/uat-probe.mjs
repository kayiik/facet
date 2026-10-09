import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { atomicJSON, runWithCheckpoints } from './uat-checkpoint.mjs';
import { evidenceFiles } from './uat-journey.mjs';

const exec = promisify(execFile);
const containerEvidence = '/home/facet/uat-evidence/journey';

// Runs inside the container with `node -e`. Lists allowlisted regular files;
// until the journey has published its final checks.json, only progress.json.
export const listing = `const fs=require('node:fs');const [dir,names]=process.argv.slice(1);let allowed=JSON.parse(names);let final=false;try{const r=JSON.parse(fs.readFileSync(dir+'/checks.json','utf8'));final=r.final===true&&r.status==='complete'}catch{}if(!final)allowed=allowed.filter(n=>n==='progress.json');console.log(JSON.stringify(allowed.flatMap(name=>{try{const s=fs.lstatSync(dir+'/'+name);return s.isFile()?[{name,signature:s.size+':'+s.mtimeMs}]:[]}catch(e){if(e.code==='ENOENT')return [];throw e}})))`;

export async function runnerDiagnostics(track, result, evidence, capture, wallNow = Date.now) {
  if (!result.timed_out && result.exit_code === 0) return;
  const diagnostic = { track, adjudication: false, observed_at: new Date(wallNow()).toISOString(), timed_out: result.timed_out, timeout_ms: result.timeout_ms, timeout_elapsed_ms: result.timeout_elapsed_ms };
  try {
    diagnostic.process_inventory = JSON.parse(await capture());
  } catch { diagnostic.process_inventory = { status: 'unavailable', reason: 'bounded-container-capture-failed', timeoutMs: 30000, processes: [] }; }
  atomicJSON(path.join(evidence, `${track}-runner-diagnostics.json`), diagnostic);
}

export async function probe() {
  // Harness 1.2.0 passes cwd, not RUN_ID/evidenceDir, to custom child commands.
  const source = process.cwd();
  const runId = process.env.UAT_RUN_ID || path.basename(path.dirname(source));
  if (!/^[a-zA-Z0-9_-]+$/.test(runId)) throw new Error('Unsafe UAT run identifier');
  const evidence = process.env.UAT_EVIDENCE_PATH || path.resolve(source, '../../../runs', runId, 'evidence');
  fs.mkdirSync(evidence, { recursive: true });
  const docker = async args => (await exec('docker', args, { encoding: 'utf8', timeout: 30000, maxBuffer: 1024 * 1024 })).stdout;
  const ids = (await docker(['ps', '--filter', `label=com.xibodev.release-harness.run-id=${runId}`, '--filter', 'label=com.docker.compose.service=facet', '--format', '{{.ID}}'])).trim().split(/\s+/).filter(Boolean);
  if (ids.length !== 1) throw new Error(`Expected exactly one running facet UAT container for this run, found ${ids.length}`);
  const container = ids[0];
  const networkNames = Object.keys(JSON.parse(await docker(['inspect', '--format', '{{json .NetworkSettings.Networks}}', container])));
  const networks = await Promise.all(networkNames.map(async name => ({ name, internal: (await docker(['network', 'inspect', '--format', '{{.Internal}}', name])).trim() === 'true' })));
  const effectiveNetwork = { container_networks: networks, internal_only: networks.length > 0 && networks.every(n => n.internal) };
  atomicJSON(path.join(evidence, 'effective-network.json'), effectiveNetwork);
  if (process.env.UAT_CERTIFY === '1' && !effectiveNetwork.internal_only) throw new Error('CERTIFICATION_REFUSED: observed UAT container network permits open egress');
  const image = (await docker(['inspect', '--format', '{{.Image}}', container])).trim();
  const started = Date.now(), copied = new Map();
  let result = null;
  const progress = phase => {
    const state = { run_id: runId, image_id: image, status: 'running', final: false, adjudication: false, phase, elapsed_ms: Date.now() - started, updated_at: new Date().toISOString(), result };
    atomicJSON(path.join(evidence, 'docker-uat.partial.json'), state);
    console.log(JSON.stringify(state));
  };
  // One metadata query per export, no repeated media copies. While the journey
  // runs only progress is publishable; the rest after its final checks.json.
  async function exportEvidence() {
    const files = JSON.parse(await docker(['exec', container, 'node', '-e', listing, containerEvidence, JSON.stringify(evidenceFiles)]));
    const target = path.join(evidence, 'journey');
    fs.mkdirSync(target, { recursive: true });
    for (const { name, signature } of files) {
      if (!evidenceFiles.includes(name)) throw new Error('Unexpected export path');
      if (copied.get(name) === signature) continue;
      const destination = path.join(target, name);
      await docker(['cp', `${container}:${containerEvidence}/${name}`, `${destination}.copying`]);
      fs.renameSync(`${destination}.copying`, destination);
      copied.set(name, signature);
    }
  }
  progress('journey');
  result = await runWithCheckpoints('docker', ['exec', container, 'node', '/opt/uat/uat-journey.mjs', containerEvidence], {
    timeout: 900000,
    checkpoint: async () => { progress('journey'); await exportEvidence(); },
  });
  // Killing host docker exec does not stop the container worker. Capture in
  // that namespace, as its configured user, before returning to core sealing.
  await runnerDiagnostics('journey', result, evidence, () => docker(['exec', container, 'node', '/opt/uat/uat-checkpoint-process.mjs']));
  // Export only after the child closed AND its final checks are published.
  await exportEvidence();
  progress('journey-exported');
  atomicJSON(path.join(evidence, 'docker-uat.json'), { run_id: runId, image_id: image, effective_network: effectiveNetwork, status: 'complete', final: true, result });
  console.log(JSON.stringify({ run_id: runId, result }));
  if (result.exit_code !== 0 || result.checkpoint_failed) process.exitCode = 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  probe().catch(() => { console.error('UAT probe failed; partial evidence is not adjudication'); process.exitCode = 1; });
}
