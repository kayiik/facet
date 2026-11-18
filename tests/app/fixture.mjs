// Only external HTTP boundaries are scripted. The window, kernel, hook,
// policy and Facet tools all run as shipped, in a disposable user profile.
import { test as base, expect } from "playwright/test";
import { createServer } from "node:http";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import { access, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export { expect };

async function binary(name) {
  if (!process.env[name]) throw new Error(`Set ${name} to the built executable; see tests/app/README.md.`);
  const path = resolve(process.env[name]);
  await access(path);
  return path;
}

function isolatedEnv(home) {
  const env = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (/^(path|systemroot|windir|comspec|pathext|lang|lc_all)$/i.test(key)) env[key] = value;
  }
  return {
    ...env,
    HOME: home,
    USERPROFILE: home,
    APPDATA: join(home, "roaming"),
    LOCALAPPDATA: join(home, "local"),
    XDG_CONFIG_HOME: join(home, "config"),
    XDG_DATA_HOME: join(home, "data"),
    XDG_CACHE_HOME: join(home, "cache"),
    XDG_STATE_HOME: join(home, "state"),
    TEMP: join(home, "tmp"),
    TMP: join(home, "tmp"),
  };
}

async function stop(child) {
  if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  const kill = () => {
    if (process.platform === "win32") {
      execFileSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore", timeout: 10_000 });
    } else {
      process.kill(-child.pid, "SIGKILL");
    }
  };
  if (process.platform === "win32") kill();
  else process.kill(-child.pid, "SIGTERM");
  const timer = setTimeout(kill, 10_000);
  try { await exited; } finally { clearTimeout(timer); }
}

function launch(executable, env, record) {
  const child = spawn(executable, ["--no-browser"], {
    env, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32",
  });
  const url = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("The App did not print its window address within 90 seconds.")), 90_000);
    let output = "";
    for (const stream of [child.stdout, child.stderr]) {
      stream.on("data", (chunk) => {
        record(chunk);
        output = (output + chunk).slice(-10_000);
        const match = output.match(/http:\/\/127\.0\.0\.1:\d+\/#t=[^\s]+/);
        if (match) { clearTimeout(timer); resolve(match[0]); }
      });
    }
    child.once("error", (err) => { clearTimeout(timer); reject(err); });
    child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`The App exited (${code}) before becoming available.`)); });
  });
  return { child, url };
}

export const test = base.extend({
  app: [async ({ page }, use, testInfo) => {
    const executable = await binary("FACET_APP_BINARY");
    const facet = await binary("FACET_APP_FACET");
    const kernel = await binary("FACET_APP_KERNEL");
    const temp = process.platform === "win32" ? join(tmpdir(), "opencode") : tmpdir();
    await mkdir(temp, { recursive: true });
    const home = await mkdtemp(join(temp, "facet-app-smoke-"));
    const env = isolatedEnv(home);
    const data = join(home, "app");
    for (const dir of Object.values(env).filter((value) => value.startsWith(home))) {
      await mkdir(dir, { recursive: true });
    }
    const errors = [];
    const pageErrors = [];
    const providerCalls = [];
    const slow = { started: 0, closed: 0 };
    page.on("pageerror", (err) => pageErrors.push(err.message));
    const server = createServer(async (req, res) => {
      try {
        let body = "";
        for await (const chunk of req) body += chunk;
        const request = JSON.parse(body);
        if (req.method === "POST" && req.url === "/v1/images/generations") {
          expect(req.headers.authorization).toBe("Bearer facet-smoke-provider-key");
          providerCalls.push(request);
          // A real one-pixel PNG; no cloud provider or media runtime needed.
          res.setHeader("Content-Type", "application/json");
          res.end(JSON.stringify({ data: [{ b64_json: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jM1cAAAAASUVORK5CYII=" }] }));
          return;
        }
        if (req.method !== "POST" || req.url !== "/v1/chat/completions") {
          throw new Error(`Unexpected fixture request: ${req.method} ${req.url}`);
        }
        expect(req.headers.authorization).toBe("Bearer facet-smoke-model-key");
        let scenario = "hello";
        for (const message of request.messages) {
          const found = message.role === "user" && typeof message.content === "string" &&
            message.content.match(/SCENARIO ([a-z0-9-]+)/);
          if (found) scenario = found[1];
        }
        const last = request.messages.at(-1);
        let text = "Hello from the local model.";
        let call;
        if (last.role === "tool") {
          text = `Done ${scenario}. Tool said: ${String(last.content).slice(0, 1000)}`;
        } else if (scenario.startsWith("paid-")) {
          call = {
            id: `call_${scenario}`, type: "function",
            function: {
              name: "mcp_facet_openai_image",
              arguments: JSON.stringify({ prompt: scenario, output_path: `renders/${scenario}.png` }),
            },
          };
        }
        const finish = call ? "tool_calls" : "stop";
        if (!request.stream) {
          res.setHeader("Content-Type", "application/json");
          res.end(JSON.stringify({ choices: [{
            index: 0, finish_reason: finish,
            message: call ? { role: "assistant", content: null, tool_calls: [call] } : { role: "assistant", content: text },
          }] }));
          return;
        }
        res.setHeader("Content-Type", "text/event-stream");
        const send = (delta, finish_reason = null) =>
          res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
        if (scenario === "slow") {
          slow.started++;
          res.once("close", () => slow.closed++);
          send({ role: "assistant", content: "Waiting for Stop." });
          return; // Hold the stream until Stop cancels the real upstream request.
        }
        send(call ? { role: "assistant", tool_calls: [{ index: 0, ...call }] } : { role: "assistant", content: text });
        send({}, finish);
        res.end("data: [DONE]\n\n");
      } catch (err) {
        errors.push(err.message);
        res.writeHead(500).end("Fixture error");
      }
    });
    let child;
    let output = "";
    let failed = false;
    try {
      await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(0, "127.0.0.1", resolve);
      });
      const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
      const started = launch(executable, {
        ...env, FACET_APP_HOME: data, FACET_APP_FACET: facet, FACET_APP_KERNEL: kernel,
        OPENAI_BASE_URL: new URL(endpoint).origin, OPENAI_API_KEY: "facet-smoke-provider-key",
      }, (chunk) => { output = (output + chunk).slice(-200_000); });
      child = started.child;
      const url = await started.url;
      const origin = new URL(url).origin;
      const token = new URLSearchParams(new URL(url).hash.slice(1)).get("t");
      await use({ url, origin, token, endpoint, providerCalls, slow });
      expect(pageErrors, "uncaught errors in the App window").toEqual([]);
      expect(errors, "unexpected requests to the local fixtures").toEqual([]);
    } catch (err) {
      failed = true;
      throw err;
    } finally {
      try {
        await stop(child);
      } finally {
        server.closeAllConnections();
        await new Promise((resolve) => server.close(resolve));
        if (failed || testInfo.status !== testInfo.expectedStatus) {
          await testInfo.attach("app.log", { body: output, contentType: "text/plain" });
          const log = await readFile(join(data, "logs", "kernel.log")).catch(() => Buffer.from("No kernel log."));
          await testInfo.attach("kernel.log", { body: log, contentType: "text/plain" });
        }
        await rm(home, { recursive: true, force: true, maxRetries: 3 });
      }
    }
  }, { timeout: 120_000 }],
});
