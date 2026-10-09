import assert from "node:assert/strict";
import { copyFile, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "playwright";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sourcePath = path.join(root, "brand", "og", "og-default.svg");
const outputPath = path.join(root, "brand", "og", "og-default.png");
const projectionPath = path.join(root, "docs", "og-default.png");
const checkOnly = process.argv.includes("--check");

const source = await readFile(sourcePath);
const browser = await chromium.launch({
  headless: true,
  args: ["--disable-gpu", "--force-color-profile=srgb"],
});

let png;
try {
  const page = await browser.newPage({
    viewport: { width: 1200, height: 630 },
    deviceScaleFactor: 1,
  });
  const dataUrl = `data:image/svg+xml;base64,${source.toString("base64")}`;

  await page.setContent(`<!DOCTYPE html>
<style>
  html, body { margin: 0; width: 1200px; height: 630px; overflow: hidden; }
  img { display: block; width: 1200px; height: 630px; }
</style>
<img src="${dataUrl}" alt="">`);

  const image = page.locator("img");
  await image.evaluate((element) => element.decode());
  png = await image.screenshot({
    type: "png",
    animations: "disabled",
    caret: "hide",
    scale: "css",
  });
} finally {
  await browser.close();
}

const pngSignature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
assert.deepEqual(png.subarray(0, 8), pngSignature, "renderer did not return a PNG");
assert.equal(png.toString("ascii", 12, 16), "IHDR", "PNG has no IHDR chunk");
assert.equal(png.readUInt32BE(16), 1200, "PNG width must be 1200 pixels");
assert.equal(png.readUInt32BE(20), 630, "PNG height must be 630 pixels");

if (checkOnly) {
  assert.deepEqual(await readFile(outputPath), png, "brand PNG is not current");
  assert.deepEqual(await readFile(projectionPath), png, "docs PNG is not current");
  console.log("Brand OG PNG is current (1200x630).");
} else {
  await writeFile(outputPath, png);
  await copyFile(outputPath, projectionPath);
  console.log("Generated brand/og/og-default.png and docs/og-default.png (1200x630).");
}
