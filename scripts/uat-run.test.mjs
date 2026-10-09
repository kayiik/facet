import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { configureRun } from './uat-run.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const evidence = path.join(os.tmpdir(), 'facet-uat-options-test');

test('runs drop inherited credentials, model configuration and runtime overrides', () => {
  const { env, certify } = configureRun([evidence], {
    PATH: 'kept', EXAMPLE_API_KEY: 'synthetic', GITHUB_TOKEN: 'synthetic',
    OPENCODE_CONFIG: '/host-home/config', OPENCODE_CONFIG_CONTENT: '{}',
    COMPOSE_FILE: 'host.yml', UAT_NETWORK_INTERNAL: 'false', UAT_CERTIFY: '1',
    UAT_OPENCODE_CONFIG_FILE: '/not-read', UAT_REAL_AGENT: '1',
  });
  assert.equal(certify, false);
  assert.deepEqual(env, { PATH: 'kept', UAT_PORT: '31000', UAT_APP_URL: 'http://127.0.0.1:31000', UAT_CERTIFY: '0' });
});

test('certification is an explicit flag in any position', () => {
  for (const args of [[evidence, '--certify'], ['--certify', evidence]]) {
    const { env, certify, evidence: resolved } = configureRun(args, {});
    assert.equal(certify, true);
    assert.equal(env.UAT_CERTIFY, '1');
    assert.equal(resolved, path.resolve(evidence));
  }
});

test('unsupported, duplicate or unsafe arguments fail before effects', () => {
  for (const args of [[], [evidence, '--unknown'], [evidence, '--certify', '--certify'], [evidence, 'extra'], [evidence, '--free-model'], ['--certify']]) {
    assert.throws(() => configureRun(args, {}), /Usage/, JSON.stringify(args));
  }
  assert.throws(() => configureRun([root], {}), /outside/);
  assert.throws(() => configureRun([path.join(root, 'new-evidence')], {}), /outside/);
});

test('the launcher refuses an in-repository evidence root before creating files or running Docker', () => {
  const inside = path.join(root, 'uat-evidence-must-not-exist');
  const result = spawnSync(process.execPath, [fileURLToPath(new URL('./uat-run.mjs', import.meta.url)), inside, '--certify'], { encoding: 'utf8', timeout: 10000 });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /outside the source repository/);
  assert.ok(!result.stdout.includes('UAT mode'));
  assert.equal(fs.existsSync(inside), false);
});
