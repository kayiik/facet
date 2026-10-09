import assert from "node:assert/strict";
import test from "node:test";

import {
  COMPOSITION_IDS,
  CUT_TYPES,
  DEFAULT_COMPOSITION,
  THEME_IDS,
  validateComposition,
  validateExplainer,
} from "../dist/contract.js";
import { THEMES } from "../dist/themes.js";

const textCut = { id: "intro", type: "text_card", text: "Facet explains", in_seconds: 0, out_seconds: 2 };

const cutsByType = {
  text_card: { text: "A" },
  hero_title: { text: "A", heroSubtitle: "B" },
  section_title: { text: "A" },
  callout: { text: "A", callout_type: "tip" },
  stat_card: { stat: "42%" },
  stat_reveal: { text: "42%", subtitle: "growth" },
  kpi_grid: { chartData: [{ label: "A", value: 1 }] },
  progress_bar: { progress: 60 },
  comparison: { leftLabel: "A", rightLabel: "B", leftValue: "1", rightValue: "2" },
  bar_chart: { chartData: [{ label: "A", value: 1 }] },
  line_chart: { chartSeries: [{ name: "A", data: [{ x: 1, y: 2 }] }] },
  pie_chart: { chartData: [{ label: "A", value: 1 }] },
  terminal_scene: { steps: [{ type: "command", text: "ls" }] },
  screenshot_scene: { backgroundImage: "shot.png", screenshotSteps: [{ type: "click", x: 1, y: 2 }] },
  anime_scene: { images: ["a.png"] },
  provider_chip: { providers: ["Edge"] },
  image: { source: "a.png", animation: "ken-burns" },
  video: { source: "a.mp4", source_in_seconds: 1 },
};

test("theme ids in the contract match the theme presets", () => {
  assert.deepEqual([...THEME_IDS].sort(), Object.keys(THEMES).sort());
});

test("every cut type has a valid example", () => {
  assert.deepEqual(Object.keys(cutsByType).sort(), [...CUT_TYPES].sort());
  for (const [type, fields] of Object.entries(cutsByType)) {
    const shape = validateExplainer({ cuts: [{ id: type, type, in_seconds: 0, out_seconds: 2, ...fields }] });
    assert.equal(shape.durationInFrames, 60, type);
  }
});

test("explicit profile gives an exact frame count", () => {
  const shape = validateExplainer({ width: 1280, height: 720, fps: 24, duration_seconds: 2, cuts: [textCut] });
  assert.deepEqual(shape, { width: 1280, height: 720, fps: 24, durationInFrames: 48 });
});

test("defaults and the last cut's end give the length when duration is omitted", () => {
  assert.deepEqual(validateExplainer({ cuts: [textCut] }), { ...DEFAULT_COMPOSITION, durationInFrames: 60 });
});

test("an untyped cut with a source is a media cut", () => {
  validateExplainer({ cuts: [{ id: "m", source: "clip.mp4", in_seconds: 0, out_seconds: 1 }] });
  assert.throws(() => validateExplainer({ cuts: [{ id: "m", in_seconds: 0, out_seconds: 1 }] }), /needs a type or a source/);
});

test("refuses unknown types, unknown fields and missing required fields, naming the cut", () => {
  assert.throws(() => validateExplainer({ cuts: [{ ...textCut, type: "text_cards" }] }), /cuts\[0\] \(intro\)\.type must be one of/);
  assert.throws(() => validateExplainer({ cuts: [{ ...textCut, txt: "typo" }] }), /cuts\[0\] \(intro\)\.txt is not a known field/);
  assert.throws(() => validateExplainer({ cuts: [{ id: "c", type: "comparison", leftLabel: "A", in_seconds: 0, out_seconds: 1 }] }), /rightLabel is required for comparison/);
  assert.throws(() => validateExplainer({ cuts: [{ ...textCut, text: "  " }] }), /text must be a non-blank string/);
  assert.throws(() => validateExplainer({ cuts: [textCut], extra: 1 }), /props\.extra is not a known field/);
});

test("refuses bad timing", () => {
  assert.throws(() => validateExplainer({ cuts: [{ ...textCut, in_seconds: 2, out_seconds: 1 }] }), /out_seconds must be greater than 2/);
  assert.throws(
    () => validateExplainer({ cuts: [textCut, { ...textCut, id: "two", in_seconds: 1, out_seconds: 3 }] }),
    /overlaps the previous cut/,
  );
  assert.throws(() => validateExplainer({ duration_seconds: 1, cuts: [textCut] }), /shorter than the last cut/);
  assert.throws(() => validateExplainer({ fps: 30, duration_seconds: 2.01, cuts: [textCut] }), /whole number of frames/);
  assert.throws(() => validateExplainer({ width: 1281, cuts: [textCut] }), /even/);
  assert.throws(() => validateExplainer({ cuts: [textCut, { ...textCut }] }), /repeats "intro"/);
});

test("refuses an unknown theme and unknown theme fields", () => {
  validateExplainer({ theme: "premium-minimalist", cuts: [textCut] });
  assert.throws(() => validateExplainer({ theme: "neon", cuts: [textCut] }), /theme must be one of/);
  assert.throws(() => validateExplainer({ themeConfig: { glow: 1 }, cuts: [textCut] }), /themeConfig\.glow is not a known field/);
});

test("checks transitions, camera moves, captions and audio", () => {
  validateExplainer({
    cuts: [{ ...textCut, transition: "wipe", transition_duration: 0.5 }, { id: "b", source: "b.png", animation: "zoom-in", in_seconds: 2, out_seconds: 4 }],
    captions: { words: [{ word: "Hi", startMs: 0, endMs: 300 }], wordsPerPage: 4, position: "bottom" },
    audio: {
      narration: { src: "narration.mp3" },
      music: { src: "bed.mp3", volume: 0.2, loop: true, duck: { ranges: [[0, 1.5]], level: 0.3 } },
      sfx: [{ src: "whoosh.mp3", atSeconds: 1.9 }],
    },
  });
  assert.throws(() => validateExplainer({ cuts: [{ ...textCut, transition: "spin" }] }), /transition must be one of/);
  assert.throws(() => validateExplainer({ cuts: [{ id: "b", source: "b.png", animation: "spin", in_seconds: 0, out_seconds: 1 }] }), /animation must be one of/);
  assert.throws(() => validateExplainer({ cuts: [textCut], captions: { words: [{ word: "Hi", startMs: 500, endMs: 100 }] } }), /endMs must not be before startMs/);
  assert.throws(() => validateExplainer({ cuts: [textCut], audio: { music: { src: "bed.mp3", volume: 2 } } }), /volume must be at most 1/);
  assert.throws(() => validateExplainer({ cuts: [textCut], audio: { music: { src: "bed.mp3", duck: { ranges: [[2, 1]] } } } }), /must end after it starts/);
});

test("every composition validates its own props", () => {
  const samples = {
    Explainer: { cuts: [textCut] },
    CinematicRenderer: { scenes: [{ id: "t", kind: "title", text: "Title", startSeconds: 0, durationSeconds: 2 }] },
    TalkingHead: { videoSrc: "talk.mp4", captions: [], duration_seconds: 2 },
    TitledVideo: { videoSrc: "a.mp4", tagline: "Line", taglineInSeconds: 0.5, duration_seconds: 2 },
    ProductReveal: { productImage: "p.png", productName: "P", price: "$1", tagline: "T", closer: "C" },
    ProductRevealVertical: { productImage: "p.png", productName: "P", price: "$1", tagline: "T", closer: "C" },
    CollageBurst: { backgroundSrc: "bg.mp4", curtainStartSeconds: 1, curtainEndSeconds: 2, clips: [{ src: "a.png", kind: "image", inSeconds: 0, outSeconds: 1, x: 0.5, y: 0.5, widthPct: 0.4, rotation: 0 }], duration_seconds: 3 },
    LyricOverlay: { videoSrc: "a.mp4", lyrics: [{ text: "la", inSeconds: 0, outSeconds: 1 }], duration_seconds: 2 },
    EndTag: { text: "THE END" },
    EndTagOverlay: { text: "THE END", overlay: true },
    CaptionOverlayOnly: { words: [{ word: "Hi", startMs: 0, endMs: 500 }] },
    HeroTitle: { title: "Facet" },
  };
  assert.deepEqual(Object.keys(samples).sort(), [...COMPOSITION_IDS].sort());
  for (const [id, props] of Object.entries(samples)) {
    const shape = validateComposition(id, props);
    assert.ok(shape.durationInFrames >= 1, id);
  }
  assert.throws(() => validateComposition("TalkingHead", { videoSrc: "a.mp4", captions: [] }), /duration_seconds is required for TalkingHead/);
  assert.throws(() => validateComposition("HeroTitle", { title: "x", sub: "y" }), /props\.sub is not a known field/);
  assert.throws(() => validateComposition("Slideshow", {}), /composition must be one of/);
});
