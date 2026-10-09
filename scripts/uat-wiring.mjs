import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';

// OpenCode reads JSON with comments and trailing commas. Remove both outside
// string literals; string contents are copied verbatim.
export function parseJsonc(text) {
  const source = String(text).replace(/^\uFEFF/, '');
  const skipTrivia = index => {
    for (;;) {
      while (index < source.length && /\s/.test(source[index])) index++;
      if (source.startsWith('//', index)) {
        while (index < source.length && source[index] !== '\n') index++;
      } else if (source.startsWith('/*', index)) {
        const end = source.indexOf('*/', index + 2);
        if (end < 0) throw new SyntaxError('Unterminated block comment');
        index = end + 2;
      } else {
        return index;
      }
    }
  };
  let out = '';
  for (let i = 0; i < source.length; i++) {
    const char = source[i];
    if (char === '"') {
      let j = i + 1;
      while (j < source.length && source[j] !== '"') j += source[j] === '\\' ? 2 : 1;
      out += source.slice(i, j + 1);
      i = j;
    } else if (char === '/' && (source[i + 1] === '/' || source[i + 1] === '*')) {
      out += ' ';
      i = skipTrivia(i) - 1;
    } else if (char === ',' && '}]'.includes(source[skipTrivia(i + 1)] ?? 'x')) {
      // Trailing comma before a closing bracket.
    } else {
      out += char;
    }
  }
  return JSON.parse(out);
}

// User-scope OpenCode configuration files, in the order OpenCode merges them.
export function opencodeUserConfigFiles(home, env = process.env) {
  const directory = env.XDG_CONFIG_HOME ? path.join(env.XDG_CONFIG_HOME, 'opencode') : path.join(home, '.config', 'opencode');
  return ['config.json', 'opencode.json', 'opencode.jsonc'].map(name => path.join(directory, name)).filter(file => fs.existsSync(file));
}

// Every configured MCP server whose command runs `<dir>/facet mcp ...`.
export function facetMcpServers(config) {
  const servers = config && typeof config.mcp === 'object' && !Array.isArray(config.mcp) ? config.mcp : {};
  return Object.entries(servers).flatMap(([name, server]) => {
    const command = server?.command;
    const runsFacet = Array.isArray(command) && command.length >= 2 && command.every(part => typeof part === 'string')
      && /^facet(\.exe)?$/i.test(command[0].split(/[\\/]/).pop()) && command[1] === 'mcp';
    return runsFacet ? [{ name, server }] : [];
  });
}

// Minimal MCP stdio client: newline-delimited JSON-RPC 2.0. Performs the
// initialize handshake and lists every tool, following pagination cursors.
export async function mcpListTools(command, args, { cwd, env, timeoutMs = 30000, protocolVersion = '2025-06-18' } = {}) {
  const child = spawn(command, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'] });
  const pending = new Map();
  let buffer = '', stderr = '', nextId = 1, failure = null;
  const fail = error => {
    failure ??= error;
    for (const { reject } of pending.values()) reject(failure);
    pending.clear();
  };
  const exited = new Promise(resolve => {
    child.on('close', (code, signal) => {
      fail(new Error(`MCP server exited (code ${code}, signal ${signal})${stderr ? `: ${stderr.trim().slice(-2000)}` : ''}`));
      resolve({ code, signal });
    });
    child.on('error', error => {
      fail(new Error(`MCP server could not start: ${error.message}`));
      if (child.pid === undefined) resolve({ code: null, signal: null });
    });
  });
  child.stdin.on('error', () => {}); // A server that exits early is reported by 'close'.
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', chunk => { stderr = (stderr + chunk).slice(-16384); });
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', chunk => {
    buffer += chunk;
    for (let newline = buffer.indexOf('\n'); newline >= 0; newline = buffer.indexOf('\n')) {
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      if (!line) continue;
      let message;
      try { message = JSON.parse(line); } catch { fail(new Error(`MCP server wrote non-JSON to stdout: ${line.slice(0, 200)}`)); return; }
      if (message.method !== undefined) {
        // A server request: answer ping, decline anything else (this client
        // declares no capabilities). Notifications need no answer.
        if (message.id !== undefined) send(message.method === 'ping' ? { id: message.id, result: {} } : { id: message.id, error: { code: -32601, message: 'Method not found' } });
        continue;
      }
      const waiter = message.id !== undefined && pending.get(message.id);
      if (!waiter) continue;
      pending.delete(message.id);
      if (message.error) waiter.reject(new Error(`MCP ${waiter.method} failed: ${message.error.message ?? JSON.stringify(message.error)}`));
      else waiter.resolve(message.result);
    }
  });
  const send = message => child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', ...message })}\n`);
  const request = (method, params) => new Promise((resolve, reject) => {
    if (failure) { reject(failure); return; }
    const id = nextId++;
    pending.set(id, { resolve, reject, method });
    send({ id, method, params });
  });
  const timer = setTimeout(() => { fail(new Error(`MCP server did not answer within ${timeoutMs} ms`)); child.kill('SIGKILL'); }, timeoutMs);
  try {
    const initialized = await request('initialize', { protocolVersion, capabilities: {}, clientInfo: { name: 'facet-uat', version: '1' } });
    send({ method: 'notifications/initialized' });
    const tools = [];
    let cursor;
    do {
      const page = await request('tools/list', cursor ? { cursor } : {});
      if (!Array.isArray(page?.tools)) throw new Error('MCP tools/list returned no tools array');
      tools.push(...page.tools.map(tool => tool?.name));
      cursor = page.nextCursor;
    } while (cursor && tools.length < 10000);
    return { protocolVersion: initialized?.protocolVersion ?? null, serverInfo: initialized?.serverInfo ?? null, tools };
  } finally {
    clearTimeout(timer);
    child.stdin.end();
    const stop = setTimeout(() => child.kill('SIGKILL'), 5000);
    await exited;
    clearTimeout(stop);
  }
}
