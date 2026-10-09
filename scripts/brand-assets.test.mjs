import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (relativePath) => readFile(path.join(root, relativePath));
const readText = async (relativePath) => (await read(relativePath)).toString("utf8");

const siteProjections = {
  "docs/favicon.svg": "icons/favicon.svg",
  "docs/logo-mark.svg": "logos/mark.svg",
  "docs/og-default.svg": "og/og-default.svg",
  "docs/og-default.png": "og/og-default.png",
};

const markBearingSvgs = [
  "brand/logos/mark.svg",
  "brand/logos/mark-inverse.svg",
  "brand/logos/lockup.svg",
  "brand/logos/lockup-inverse.svg",
  "brand/logos/mono-black.svg",
  "brand/logos/mono-white.svg",
  "brand/icons/favicon.svg",
  "brand/icons/app-icon.svg",
  "brand/icons/icon-192.svg",
  "brand/icons/icon-512.svg",
  "brand/og/og-default.svg",
  "docs/favicon.svg",
  "docs/logo-mark.svg",
  "docs/og-default.svg",
];

const fullColorSvgs = markBearingSvgs.filter(
  (relativePath) => !relativePath.includes("mono-"),
);

const pieces = [
  '<path fill-rule="evenodd" d="M54 38 H89 V48 L94 52 L89 56 V68 H83 V83 H72 L68 87 L64 83 H42 V50 C42 43.37 47.37 38 54 38 Z M61 54 L83 67 L61 80 Z"/>',
  '<path d="M89 38 C95.08 38 100 42.92 100 49 V68 H89 V56 L94 52 L89 48 Z" transform="translate(2 -1.5)"/>',
  '<path d="M83 68 H100 V84 C100 90.63 94.63 96 88 96 H83 Z" transform="translate(2 2)"/>',
  '<path d="M42 83 H64 L68 87 L72 83 H83 V96 H54 C47.37 96 42 90.63 42 83 Z" transform="translate(-1 2)"/>',
];

test("site projections are byte-identical to their canonical assets", async () => {
  const provenance = JSON.parse(await readText("brand/provenance.json"));
  assert.deepEqual(provenance.siteProjections, siteProjections);

  for (const [projection, canonical] of Object.entries(siteProjections)) {
    assert.deepEqual(
      await read(projection),
      await read(path.join("brand", canonical)),
      `${projection} differs from brand/${canonical}`,
    );
  }
});

test("social PNGs are exactly 1200x630", async () => {
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

  for (const relativePath of ["brand/og/og-default.png", "docs/og-default.png"]) {
    const png = await read(relativePath);
    assert.deepEqual(png.subarray(0, 8), signature, `${relativePath} is not a PNG`);
    assert.equal(png.toString("ascii", 12, 16), "IHDR", `${relativePath} has no IHDR`);
    assert.equal(png.readUInt32BE(16), 1200, `${relativePath} width changed`);
    assert.equal(png.readUInt32BE(20), 630, `${relativePath} height changed`);
  }
});

test("every mark keeps the exact four-piece Puzzle B geometry", async () => {
  const backTile = 'x="20" y="16" width="58" height="58" rx="12"';
  const middleTile = 'x="31" y="27" width="58" height="58" rx="12"';

  for (const relativePath of markBearingSvgs) {
    const svg = await readText(relativePath);
    assert.ok(svg.includes(backTile), `${relativePath} changed the back tile`);
    assert.ok(svg.includes(middleTile), `${relativePath} changed the middle tile`);
    assert.equal((svg.match(/<path\b/g) ?? []).length, 4, `${relativePath} must have four pieces`);
    for (const piece of pieces) {
      assert.ok(svg.includes(piece), `${relativePath} changed an approved Puzzle B piece`);
    }
  }
});

test("full-color marks keep the approved signature colors", async () => {
  const backTile = '<rect x="20" y="16" width="58" height="58" rx="12" fill="#F36C7A"/>';
  const middleTile = '<rect x="31" y="27" width="58" height="58" rx="12" fill="#10B981"/>';

  for (const relativePath of fullColorSvgs) {
    const svg = await readText(relativePath);
    assert.ok(svg.includes(backTile), `${relativePath} changed the Begonia tile`);
    assert.ok(svg.includes(middleTile), `${relativePath} changed the Green tile`);
    assert.ok(svg.includes('<g fill="#D92D48">'), `${relativePath} changed the Red pieces`);
  }
});

test("homepage social metadata uses the PNG projection", async () => {
  const html = await readText("docs/index.html");
  assert.ok(html.includes('<link rel="canonical" href="https://kayiik.github.io/facet/">'));
  assert.ok(html.includes('<meta property="og:image" content="https://kayiik.github.io/facet/og-default.png">'));
  assert.ok(html.includes('<meta property="og:image:type" content="image/png">'));
  assert.ok(html.includes('<meta property="og:image:width" content="1200">'));
  assert.ok(html.includes('<meta property="og:image:height" content="630">'));
  assert.match(html, /<meta property="og:image:alt" content="[^"]+">/);
  assert.ok(html.includes('<meta name="twitter:card" content="summary_large_image">'));
  assert.ok(html.includes('<meta name="twitter:image" content="https://kayiik.github.io/facet/og-default.png">'));
  assert.match(html, /<meta name="twitter:image:alt" content="[^"]+">/);
});

test("brand preview exposes the formal product name", async () => {
  const html = await readText("brand/preview.html");
  assert.ok(html.includes('<h1 aria-label="Facet">'));
  assert.match(html, /<span class="visual-wordmark" aria-hidden="true">facet/);
});
