import assert from "node:assert/strict";
import {readFile, readdir} from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import {fileURLToPath} from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(
  await readFile(path.join(root, "composer-manifest.json"), "utf8"),
);

const listFiles = async (directory) => {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await listFiles(absolute)));
    } else {
      files.push(path.relative(root, absolute).replaceAll("\\", "/"));
    }
  }
  return files;
};

test("manifest is a sorted, duplicate-free list of composer source paths", () => {
  assert.deepEqual(Object.keys(manifest).sort(), ["allowedSourcePaths", "version"]);
  assert.equal(manifest.version, 1);
  const paths = manifest.allowedSourcePaths;
  assert.ok(Array.isArray(paths) && paths.length > 0);
  assert.deepEqual(paths, [...new Set(paths)].sort());
  for (const file of paths) {
    assert.match(file, /^src\/[A-Za-z0-9_./-]+$/, file);
    assert.ok(!file.split("/").includes(".."), file);
  }
});

test("src contains exactly the allowlisted files", async () => {
  const actual = (await listFiles(path.join(root, "src"))).sort();
  assert.deepEqual(actual, manifest.allowedSourcePaths);
});

// Remotion resets its list of render holds while its own modules load. A hold
// taken as fonts.ts loads is dropped from that list but its timer still runs,
// so every render longer than the timeout failed. The hold is taken in Root.
test("the fonts hold is taken while rendering the root, not as a module loads", async () => {
  const fonts = await readFile(path.join(root, "src", "fonts.ts"), "utf8");
  const rootSource = await readFile(path.join(root, "src", "Root.tsx"), "utf8");
  assert.doesNotMatch(fonts, /^ensureFonts\(\);?\s*$/m);
  assert.doesNotMatch(rootSource, /^import "\.\/fonts";/m);
  assert.match(rootSource, /useState\(ensureFonts\)/);
});