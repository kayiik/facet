import { test, expect } from "./fixture.mjs";

const modelKey = "facet-smoke-model-key";

async function chooseModel(page, app) {
  await page.goto(app.url);
  // The initial no-model kernel must finish starting before it is replaced.
  await expect(page.locator("#status")).toHaveText("ready", { timeout: 90_000 });
  await page.getByLabel("Endpoint", { exact: true }).fill(app.endpoint);
  await page.getByLabel("Model", { exact: true }).fill("scripted");
  await page.getByLabel("API key", { exact: true }).fill(modelKey);
  await page.getByRole("button", { name: "Save and start" }).click();
  await expect(page.locator("#model")).toHaveText("Model: scripted", { timeout: 90_000 });
  await expect(page.locator("#status")).toHaveText("ready");
}

async function send(page, text) {
  await page.getByPlaceholder("Describe the video you want, or what to change.").fill(text);
  await page.getByRole("button", { name: "Send", exact: true }).click();
}

test("projects work without a model and survive a reload", async ({ page, app }) => {
  await page.goto(app.url);
  await expect(page.getByRole("heading", { name: "Choose a model" })).toBeVisible();
  await page.getByRole("link", { name: "Projects", exact: true }).click();
  await expect(page.getByText("No projects yet.", { exact: false })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Smoke film");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Smoke Film", exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Smoke Film", exact: true })).toBeVisible();
});

test("model setup enables conversation without exposing its key", async ({ page, app }) => {
  await chooseModel(page, app);
  await send(page, "SCENARIO hello");
  await expect(page.locator("#conversation")).toContainText("Hello from the local model.");
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeDisabled();
  const state = await page.request.get(`${app.origin}/api/state`, {
    headers: { "X-Facet-Token": app.token },
  });
  expect(state.ok()).toBe(true);
  expect(await state.text()).not.toContain(modelKey);
  await expect(page.locator("body")).not.toContainText(modelKey);
  await expect(page.getByLabel("API key", { exact: true })).toHaveValue("");
  await page.reload();
  await expect(page.locator("#model")).toHaveText("Model: scripted");
  await expect(page.locator("#conversation")).toContainText("Hello from the local model.");
});

test("the local API refuses missing tokens, foreign origins and engine commands", async ({ page, app }) => {
  const headers = { "X-Facet-Token": app.token };
  expect((await page.request.get(`${app.origin}/api/state`)).status()).toBe(401);
  expect((await page.request.get(`${app.origin}/api/state?t=${app.token}`)).status()).toBe(401);
  expect((await page.request.get(`${app.origin}/api/events`)).status()).toBe(401);
  expect((await page.request.get(`${app.origin}/api/state`, {
    headers: { "X-Facet-Token": "not-the-token" },
  })).status()).toBe(401);
  expect((await page.request.get(`${app.origin}/api/state`, { headers })).status()).toBe(200);
  expect((await page.request.get(`${app.origin}/api/state`, {
    headers: { ...headers, Host: "not-facet.invalid" },
  })).status()).toBe(421);
  expect((await page.request.post(`${app.origin}/api/projects`, {
    headers: { ...headers, Origin: "https://not-facet.invalid" }, data: { name: "Unwanted project" },
  })).status()).toBe(403);
  expect((await page.request.post(`${app.origin}/api/projects`, {
    data: { name: "Unwanted project" },
  })).status()).toBe(401);
  const projects = await page.request.get(`${app.origin}/api/projects`, { headers });
  expect((await projects.json()).projects).toEqual([]);
  expect((await page.request.post(`${app.origin}/api/send`, {
    headers, data: { text: "/approve all" },
  })).status()).toBe(400);
});

test("denying a paid tool makes no provider call, even after reloading its prompt", async ({ page, app }) => {
  await chooseModel(page, app);
  await send(page, "SCENARIO paid-deny");
  await expect(page.getByRole("heading", { name: "Approve a paid tool?" })).toBeVisible();
  expect(app.providerCalls).toHaveLength(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Approve a paid tool?" })).toBeVisible();
  await page.getByRole("button", { name: "Deny", exact: true }).click();
  await expect(page.locator("#conversation")).toContainText("Done paid-deny.");
  await expect(page.locator("#conversation")).toContainText("Not approved: openai image");
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeDisabled();
  expect(app.providerCalls).toHaveLength(0);
});

test("approving a paid tool submits once and cannot replay the approval", async ({ page, app }) => {
  await chooseModel(page, app);
  await send(page, "SCENARIO paid-approve");
  await expect(page.getByRole("heading", { name: "Approve a paid tool?" })).toBeVisible();
  expect(app.providerCalls).toHaveLength(0);
  const headers = { "X-Facet-Token": app.token };
  const state = await (await page.request.get(`${app.origin}/api/state`, { headers })).json();
  expect(state.pending).toHaveLength(1);
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(page.locator("#conversation")).toContainText(/Done paid-approve\. Tool said: [\s\S]*"ok":\s*true/);
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeDisabled();
  expect(app.providerCalls).toHaveLength(1);
  expect(app.providerCalls[0]).toMatchObject({ prompt: "paid-approve", n: 1, response_format: "b64_json" });
  expect((await page.request.post(`${app.origin}/api/answer`, {
    headers, data: { id: state.pending[0].id, approve: true },
  })).status()).toBe(404);
  expect(app.providerCalls).toHaveLength(1);
});

test("Stop cancels a streamed turn and the next message still works", async ({ page, app }) => {
  await chooseModel(page, app);
  await send(page, "SCENARIO slow");
  await expect(page.locator("#conversation")).toContainText("Waiting for Stop.");
  expect(app.slow.started).toBe(1);
  await page.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(page.locator("#conversation")).toContainText("Stopped.");
  await expect.poll(() => app.slow.closed).toBe(1);
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeDisabled();
  await send(page, "SCENARIO hello");
  await expect(page.locator("#conversation")).toContainText("Hello from the local model.");
});
