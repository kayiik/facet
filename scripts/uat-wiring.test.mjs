import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { facetMcpServers, mcpListTools, opencodeUserConfigFiles, parseJsonc } from './uat-wiring.mjs';

// A synthetic stdio MCP server. It is a protocol fixture for the client only;
// the Docker journey talks to the installed `facet mcp`. Before the first
// tools/list page it sends the client two requests and checks the answers.
const server = `
const readline = require('node:readline');
const mode = process.argv[1];
const pages = [[{ name: 'video_compose' }, { name: 'output_review' }], [{ name: 'media_probe' }]];
const send = message => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\\n');
const answers = new Map();
let deferred = null;
const page = (message, extra = []) => {
  const index = message.params?.cursor ? Number(message.params.cursor) : 0;
  send({ id: message.id, result: { tools: [...pages[index], ...extra], ...(index + 1 < pages.length ? { nextCursor: String(index + 1) } : {}) } });
};
readline.createInterface({ input: process.stdin }).on('line', line => {
  const message = JSON.parse(line);
  if (message.method === undefined) {
    answers.set(message.id, message);
    if (deferred && answers.size === 2) {
      const ok = JSON.stringify(answers.get('srv-ping').result) === '{}' && answers.get('srv-roots').error?.code === -32601;
      page(deferred, [{ name: ok ? 'client-answered-requests' : 'client-answers-wrong' }]);
    }
    return;
  }
  if (message.method === 'initialize') {
    if (mode === 'hang') return;
    if (mode === 'crash') { process.stderr.write('synthetic startup failure'); process.exit(3); }
    send({ method: 'notifications/message', params: { level: 'info', data: 'ready' } });
    return send({ id: message.id, result: { protocolVersion: message.params.protocolVersion, serverInfo: { name: 'synthetic', version: '0.0.0' }, capabilities: { tools: {} } } });
  }
  if (message.method === 'tools/list') {
    if (mode === 'error') return send({ id: message.id, error: { code: -32601, message: 'synthetic method failure' } });
    if (!message.params?.cursor) {
      deferred = message;
      send({ id: 'srv-ping', method: 'ping' });
      return send({ id: 'srv-roots', method: 'roots/list' });
    }
    page(message);
  }
});
`;

test('JSONC parsing removes comments and trailing commas but never string content', () => {
  const text = '\uFEFF{\n  // line comment\n  "$schema": "https://opencode.ai/config.json", /* block */\n  "mcp": {\n    "facet": { "type": "local", "command": ["/a,}/facet", "mcp",], "note": "keep // and /* and ,] here" },\n  },\n}\n';
  assert.deepEqual(parseJsonc(text), {
    $schema: 'https://opencode.ai/config.json',
    mcp: { facet: { type: 'local', command: ['/a,}/facet', 'mcp'], note: 'keep // and /* and ,] here' } },
  });
  assert.deepEqual(parseJsonc('{"escaped": "quote \\" // not a comment"}'), { escaped: 'quote " // not a comment' });
  assert.throws(() => parseJsonc('{ /* unterminated'), SyntaxError);
  assert.throws(() => parseJsonc('{"a": }'), SyntaxError);
});

test('facet MCP entries are recognized by executable name and mcp subcommand only', () => {
  const config = {
    mcp: {
      facet: { type: 'local', command: ['/home/facet/.facet/current/bin/facet', 'mcp'] },
      windows: { type: 'local', command: ['C:\\Users\\Test User\\.facet\\current\\bin\\facet.exe', 'mcp', '--root', 'C:\\work'] },
      bare: { type: 'local', command: ['facet', 'mcp'] },
      other: { type: 'local', command: ['npx', '-y', 'facet', 'mcp'] },
      lookalike: { type: 'local', command: ['/usr/bin/facet-mcp', 'mcp'] },
      tools: { type: 'local', command: ['/usr/bin/facet', 'tools', 'list'] },
      remote: { type: 'remote', url: 'https://example.invalid/mcp' },
    },
  };
  assert.deepEqual(facetMcpServers(config).map(entry => entry.name), ['facet', 'windows', 'bare']);
  for (const empty of [null, {}, { mcp: [] }, { mcp: 'facet' }]) assert.deepEqual(facetMcpServers(empty), []);
});

test('user-scope OpenCode configuration honours XDG_CONFIG_HOME', t => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'facet-uat-wiring-'));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  const fallback = path.join(home, '.config', 'opencode');
  fs.mkdirSync(fallback, { recursive: true });
  fs.writeFileSync(path.join(fallback, 'opencode.jsonc'), '{}');
  fs.writeFileSync(path.join(fallback, 'opencode.json'), '{}');
  assert.deepEqual(opencodeUserConfigFiles(home, {}), [path.join(fallback, 'opencode.json'), path.join(fallback, 'opencode.jsonc')]);
  const xdg = path.join(home, 'xdg');
  fs.mkdirSync(path.join(xdg, 'opencode'), { recursive: true });
  fs.writeFileSync(path.join(xdg, 'opencode', 'config.json'), '{}');
  assert.deepEqual(opencodeUserConfigFiles(home, { XDG_CONFIG_HOME: xdg }), [path.join(xdg, 'opencode', 'config.json')]);
});

test('MCP client completes the handshake, answers server requests and follows pagination', async () => {
  const result = await mcpListTools(process.execPath, ['-e', server, 'ok'], { timeoutMs: 10000 });
  assert.equal(result.protocolVersion, '2025-06-18');
  assert.deepEqual(result.serverInfo, { name: 'synthetic', version: '0.0.0' });
  assert.deepEqual(result.tools, ['video_compose', 'output_review', 'client-answered-requests', 'media_probe']);
});

test('MCP client fails closed on errors, crashes, silence and missing executables', async () => {
  await assert.rejects(mcpListTools(process.execPath, ['-e', server, 'error'], { timeoutMs: 10000 }), /tools\/list failed: synthetic method failure/);
  await assert.rejects(mcpListTools(process.execPath, ['-e', server, 'crash'], { timeoutMs: 10000 }), /exited \(code 3.*synthetic startup failure/);
  await assert.rejects(mcpListTools(process.execPath, ['-e', server, 'hang'], { timeoutMs: 500 }), /did not answer within 500 ms/);
  await assert.rejects(mcpListTools(path.join(os.tmpdir(), 'facet-uat-missing-executable'), [], { timeoutMs: 10000 }), /could not start/);
});
