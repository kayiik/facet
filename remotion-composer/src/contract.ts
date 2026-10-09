// Strict props contract for every Facet composition.
//
// Every composition's props are checked before a single frame renders: known
// fields only, required fields per scene type, types, ranges and timing. A
// missing or misspelled field is an error that names the cut and the field,
// never an empty frame. This file has no imports so the contract tests can load
// the compiled module directly in Node.

export const DEFAULT_COMPOSITION = { width: 1920, height: 1080, fps: 30 };

export const THEME_IDS = [
  "clean-professional",
  "flat-motion-graphics",
  "minimalist-diagram",
  "premium-minimalist",
  "anime-ghibli",
];

export const THEME_FIELDS = [
  "primaryColor", "accentColor", "backgroundColor", "surfaceColor", "textColor",
  "mutedTextColor", "headingFont", "bodyFont", "monoFont", "chartColors",
  "springConfig", "transitionDuration", "captionHighlightColor", "captionBackgroundColor",
];

export const TRANSITION_KINDS = ["cut", "fade", "slide", "wipe", "flip", "clock-wipe", "iris"];

export const CAMERA_MOVES = [
  "zoom-in", "zoom-out", "pan-left", "pan-right", "ken-burns", "ken-burns-slow-zoom",
  "parallax", "static", "none", "drift-up", "drift-down",
];

// Explainer cut types and the fields each one cannot do without.
export const CUT_REQUIRED: Record<string, string[]> = {
  text_card: ["text"],
  hero_title: ["text"],
  section_title: ["text"],
  callout: ["text"],
  stat_card: ["stat"],
  stat_reveal: ["text"],
  kpi_grid: ["chartData"],
  progress_bar: ["progress"],
  comparison: ["leftLabel", "rightLabel", "leftValue", "rightValue"],
  bar_chart: ["chartData"],
  line_chart: ["chartSeries"],
  pie_chart: ["chartData"],
  terminal_scene: ["steps"],
  screenshot_scene: ["backgroundImage", "screenshotSteps"],
  anime_scene: ["images"],
  provider_chip: ["providers"],
  image: ["source"],
  video: ["source"],
};

export const CUT_TYPES = Object.keys(CUT_REQUIRED);

export const OVERLAY_TYPES = ["section_title", "stat_reveal", "hero_title", "provider_chip"];

export const COMPOSITION_IDS = [
  "Explainer", "CinematicRenderer", "TalkingHead", "TitledVideo", "ProductReveal",
  "ProductRevealVertical", "CollageBurst", "LyricOverlay", "EndTag", "EndTagOverlay",
  "CaptionOverlayOnly", "HeroTitle",
];

const CUT_FIELDS = new Set([
  "id", "type", "source", "media_kind", "in_seconds", "out_seconds", "source_in_seconds",
  "animation", "transition", "transition_duration",
  "text", "stat", "subtitle", "callout_type", "title", "heroSubtitle",
  "leftLabel", "rightLabel", "leftValue", "rightValue",
  "chartData", "chartSeries", "chartColors", "chartAnimation", "donut", "centerLabel",
  "centerValue", "showGrid", "showValues", "showLegend", "showMarkers", "xLabel", "yLabel", "columns",
  "progress", "progressLabel", "progressColor", "progressAnimation", "progressSegments",
  "backgroundColor", "cardBackgroundColor", "backgroundImage", "backgroundVideo",
  "backgroundVideoStart", "backgroundOverlay", "color", "accentColor", "fontSize",
  "images", "particles", "particleColor", "particleCount", "particleIntensity", "vignette",
  "lightingFrom", "lightingTo",
  "steps", "terminalTitle", "prompt",
  "screenshotSteps", "screenshotSize", "cursorStartAt",
  "providers", "cycleSeconds", "label", "position",
]);

const OVERLAY_FIELDS = new Set([
  "type", "in_seconds", "out_seconds", "text", "subtitle", "accentColor", "position",
  "providers", "cycleSeconds", "label",
]);

export class ContractError extends Error {}

type Rec = Record<string, unknown>;

export interface Shape {
  width: number;
  height: number;
  fps: number;
  durationInFrames: number;
}

const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);

function fail(path: string, message: string): never {
  throw new ContractError(`${path} ${message}`);
}

function record(v: unknown, path: string): Rec {
  if (!isRec(v)) fail(path, "must be an object");
  return v;
}

function known(obj: Rec, allowed: Set<string> | string[], path: string): void {
  const set = allowed instanceof Set ? allowed : new Set(allowed);
  for (const key of Object.keys(obj)) {
    if (!set.has(key)) fail(`${path}.${key}`, "is not a known field");
  }
}

function num(v: unknown, path: string, opts: { min?: number; max?: number; int?: boolean; above?: number } = {}): number {
  if (typeof v !== "number" || !Number.isFinite(v)) fail(path, "must be a finite number");
  if (opts.int && !Number.isInteger(v)) fail(path, "must be an integer");
  if (opts.min !== undefined && v < opts.min) fail(path, `must be at least ${opts.min}`);
  if (opts.above !== undefined && v <= opts.above) fail(path, `must be greater than ${opts.above}`);
  if (opts.max !== undefined && v > opts.max) fail(path, `must be at most ${opts.max}`);
  return v;
}

function optNum(obj: Rec, key: string, path: string, opts: { min?: number; max?: number; int?: boolean; above?: number } = {}): void {
  if (obj[key] !== undefined) num(obj[key], `${path}.${key}`, opts);
}

function text(v: unknown, path: string): string {
  if (typeof v !== "string" || v.trim() === "") fail(path, "must be a non-blank string");
  return v;
}

function optText(obj: Rec, key: string, path: string): void {
  if (obj[key] !== undefined) text(obj[key], `${path}.${key}`);
}

function optBool(obj: Rec, key: string, path: string): void {
  if (obj[key] !== undefined && typeof obj[key] !== "boolean") fail(`${path}.${key}`, "must be true or false");
}

function oneOf(v: unknown, values: string[], path: string): string {
  if (typeof v !== "string" || !values.includes(v)) fail(path, `must be one of ${values.join(", ")}`);
  return v;
}

function array(v: unknown, path: string, nonEmpty = true): unknown[] {
  if (!Array.isArray(v)) fail(path, "must be an array");
  if (nonEmpty && v.length === 0) fail(path, "must not be empty");
  return v;
}

// frame validates width, height, fps and an explicit duration, returning the
// frame shape with the duration still to be decided by the caller.
function frame(props: Rec, defaults: { width: number; height: number; fps: number }) {
  const width = props.width === undefined ? defaults.width : num(props.width, "width", { int: true, above: 0 });
  const height = props.height === undefined ? defaults.height : num(props.height, "height", { int: true, above: 0 });
  if (width % 2 !== 0 || height % 2 !== 0) fail("width/height", "must be even numbers");
  const fps = props.fps === undefined ? defaults.fps : num(props.fps, "fps", { above: 0, max: 120 });
  const duration = props.duration_seconds === undefined ? undefined : num(props.duration_seconds, "duration_seconds", { above: 0 });
  return { width, height, fps, duration };
}

function frames(seconds: number, fps: number, path: string): number {
  const exact = seconds * fps;
  const rounded = Math.round(exact);
  if (Math.abs(exact - rounded) > 1e-6) fail(path, `times fps must be a whole number of frames (got ${exact})`);
  if (rounded < 1) fail(path, "must last at least one frame");
  return rounded;
}

function theme(props: Rec): void {
  if (props.theme !== undefined) oneOf(props.theme, THEME_IDS, "theme");
  if (props.themeConfig !== undefined) known(record(props.themeConfig, "themeConfig"), THEME_FIELDS, "themeConfig");
}

// nonEmpty is false where a video may carry no captions at all (TalkingHead).
function words(v: unknown, path: string, nonEmpty = true): void {
  array(v, path, nonEmpty).forEach((w, i) => {
    const p = `${path}[${i}]`;
    const word = record(w, p);
    known(word, ["word", "startMs", "endMs", "pageBreakAfter"], p);
    text(word.word, `${p}.word`);
    const start = num(word.startMs, `${p}.startMs`, { min: 0 });
    const end = num(word.endMs, `${p}.endMs`, { min: 0 });
    if (end < start) fail(`${p}.endMs`, "must not be before startMs");
    optBool(word, "pageBreakAfter", p);
  });
}

function captions(v: unknown): void {
  const c = record(v, "captions");
  known(c, ["words", "wordsPerPage", "highlightColor", "position", "fontSize"], "captions");
  words(c.words, "captions.words");
  optNum(c, "wordsPerPage", "captions", { int: true, min: 1, max: 12 });
  optText(c, "highlightColor", "captions");
  if (c.position !== undefined) oneOf(c.position, ["bottom", "center", "top"], "captions.position");
  optNum(c, "fontSize", "captions", { above: 0 });
}

function audio(v: unknown): void {
  const a = record(v, "audio");
  known(a, ["narration", "music", "sfx"], "audio");
  if (a.narration !== undefined) {
    const n = record(a.narration, "audio.narration");
    known(n, ["src", "volume"], "audio.narration");
    text(n.src, "audio.narration.src");
    optNum(n, "volume", "audio.narration", { min: 0, max: 1 });
  }
  if (a.music !== undefined) {
    const m = record(a.music, "audio.music");
    known(m, ["src", "volume", "offsetSeconds", "loop", "fadeInSeconds", "fadeOutSeconds", "duck"], "audio.music");
    text(m.src, "audio.music.src");
    optNum(m, "volume", "audio.music", { min: 0, max: 1 });
    optNum(m, "offsetSeconds", "audio.music", { min: 0 });
    optBool(m, "loop", "audio.music");
    optNum(m, "fadeInSeconds", "audio.music", { min: 0 });
    optNum(m, "fadeOutSeconds", "audio.music", { min: 0 });
    if (m.duck !== undefined) {
      const d = record(m.duck, "audio.music.duck");
      known(d, ["ranges", "level", "attackSeconds", "releaseSeconds"], "audio.music.duck");
      array(d.ranges, "audio.music.duck.ranges", false).forEach((r, i) => {
        const p = `audio.music.duck.ranges[${i}]`;
        const pair = array(r, p);
        if (pair.length !== 2) fail(p, "must be [startSeconds, endSeconds]");
        const s = num(pair[0], `${p}[0]`, { min: 0 });
        const e = num(pair[1], `${p}[1]`, { min: 0 });
        if (e < s) fail(p, "must end after it starts");
      });
      optNum(d, "level", "audio.music.duck", { min: 0, max: 1 });
      optNum(d, "attackSeconds", "audio.music.duck", { min: 0 });
      optNum(d, "releaseSeconds", "audio.music.duck", { min: 0 });
    }
  }
  if (a.sfx !== undefined) {
    array(a.sfx, "audio.sfx", false).forEach((cue, i) => {
      const p = `audio.sfx[${i}]`;
      const c = record(cue, p);
      known(c, ["src", "atSeconds", "volume"], p);
      text(c.src, `${p}.src`);
      num(c.atSeconds, `${p}.atSeconds`, { min: 0 });
      optNum(c, "volume", p, { min: 0, max: 1 });
    });
  }
}

function cutType(cut: Rec, path: string): string {
  if (cut.type === undefined) {
    if (cut.source === undefined) fail(path, "needs a type or a source");
    if (cut.media_kind !== undefined) return oneOf(cut.media_kind, ["image", "video"], `${path}.media_kind`);
    return "image";
  }
  return oneOf(cut.type, CUT_TYPES, `${path}.type`);
}

function cut(raw: unknown, i: number): { id: string; start: number; end: number } {
  const c = record(raw, `cuts[${i}]`);
  const id = c.id === undefined ? `cut-${i + 1}` : text(c.id, `cuts[${i}].id`);
  const path = `cuts[${i}] (${id})`;
  known(c, CUT_FIELDS, path);
  const type = cutType(c, path);
  for (const field of CUT_REQUIRED[type]) {
    if (c[field] === undefined) fail(`${path}.${field}`, `is required for ${type}`);
  }
  const start = num(c.in_seconds, `${path}.in_seconds`, { min: 0 });
  const end = num(c.out_seconds, `${path}.out_seconds`, { above: start });
  for (const key of ["text", "stat", "source", "leftLabel", "rightLabel", "leftValue", "rightValue", "backgroundImage", "backgroundVideo", "title", "subtitle", "heroSubtitle", "label"]) {
    optText(c, key, path);
  }
  if (c.media_kind !== undefined) oneOf(c.media_kind, ["image", "video"], `${path}.media_kind`);
  if (c.animation !== undefined) oneOf(c.animation, CAMERA_MOVES, `${path}.animation`);
  if (c.transition !== undefined) oneOf(c.transition, TRANSITION_KINDS, `${path}.transition`);
  optNum(c, "transition_duration", path, { above: 0, max: 3 });
  optNum(c, "source_in_seconds", path, { min: 0 });
  optNum(c, "progress", path, { min: 0, max: 100 });
  optNum(c, "fontSize", path, { above: 0 });
  optNum(c, "backgroundOverlay", path, { min: 0, max: 1 });
  if (c.callout_type !== undefined) oneOf(c.callout_type, ["info", "warning", "tip", "quote"], `${path}.callout_type`);
  for (const key of ["chartData", "chartSeries", "steps", "screenshotSteps", "images", "providers"]) {
    if (c[key] !== undefined) {
      const items = array(c[key], `${path}.${key}`);
      items.forEach((item, k) => {
        if (key === "images" || key === "providers") text(item, `${path}.${key}[${k}]`);
        else record(item, `${path}.${key}[${k}]`);
      });
    }
  }
  return { id, start, end };
}

// validateExplainer checks Explainer props and returns the frame shape.
// Duration: duration_seconds when given, otherwise the last cut's out_seconds.
export function validateExplainer(input: unknown): Shape {
  const props = record(input, "props");
  known(props, ["cuts", "overlays", "captions", "audio", "width", "height", "fps", "duration_seconds", "theme", "themeConfig"], "props");
  const f = frame(props, DEFAULT_COMPOSITION);
  theme(props);
  const cuts = array(props.cuts, "cuts");
  const ids = new Set<string>();
  let previousEnd = 0;
  let lastEnd = 0;
  cuts.forEach((raw, i) => {
    const { id, start, end } = cut(raw, i);
    if (ids.has(id)) fail(`cuts[${i}].id`, `repeats "${id}"`);
    ids.add(id);
    if (start + 1e-9 < previousEnd) fail(`cuts[${i}] (${id}).in_seconds`, `overlaps the previous cut, which ends at ${previousEnd}`);
    if (Math.round(end * f.fps) - Math.round(start * f.fps) < 1) fail(`cuts[${i}] (${id})`, "must last at least one frame");
    previousEnd = end;
    lastEnd = Math.max(lastEnd, end);
  });
  const duration = f.duration ?? lastEnd;
  if (lastEnd > duration + 1e-9) fail("duration_seconds", `is shorter than the last cut, which ends at ${lastEnd}`);
  if (props.overlays !== undefined) {
    array(props.overlays, "overlays", false).forEach((raw, i) => {
      const p = `overlays[${i}]`;
      const o = record(raw, p);
      known(o, OVERLAY_FIELDS, p);
      const type = oneOf(o.type, OVERLAY_TYPES, `${p}.type`);
      if (type === "provider_chip") array(o.providers, `${p}.providers`).forEach((x, k) => text(x, `${p}.providers[${k}]`));
      else text(o.text, `${p}.text`);
      const start = num(o.in_seconds, `${p}.in_seconds`, { min: 0 });
      const end = num(o.out_seconds, `${p}.out_seconds`, { above: start });
      if (end > duration + 1e-9) fail(`${p}.out_seconds`, `is after the end of the video (${duration})`);
    });
  }
  if (props.captions !== undefined) captions(props.captions);
  if (props.audio !== undefined) audio(props.audio);
  // An explicit duration must be a whole number of frames; the default (the
  // last cut's end) is rounded to the nearest frame.
  const durationInFrames = f.duration !== undefined
    ? frames(f.duration, f.fps, "duration_seconds")
    : Math.max(1, Math.round(lastEnd * f.fps));
  return { width: f.width, height: f.height, fps: f.fps, durationInFrames };
}

interface Spec {
  fields: string[];
  required: string[];
  size: { width: number; height: number };
  // seconds when no duration_seconds is given; undefined = duration_seconds required
  duration?: (props: Rec) => number;
}

const FRAME_FIELDS = ["width", "height", "fps", "duration_seconds"];

const SPECS: Record<string, Spec> = {
  CinematicRenderer: {
    fields: ["scenes", "titleFontSize", "titleWidth", "signalLineCount", "soundtrack", "music", "captions"],
    required: ["scenes"],
    size: { width: 1920, height: 1080 },
    duration: (p) => Math.max(0, ...(p.scenes as Rec[]).map((s) => (s.startSeconds as number) + (s.durationSeconds as number))),
  },
  TalkingHead: {
    fields: ["videoSrc", "captions", "overlays", "wordsPerPage", "fontSize", "highlightColor", "captionColor", "captionBackgroundColor", "captionFontFamily", "captionWordSeparator"],
    required: ["videoSrc", "captions"],
    size: { width: 1080, height: 1920 },
  },
  TitledVideo: {
    fields: ["videoSrc", "tagline", "taglineInSeconds", "taglineOutSeconds", "topPx", "fontSize", "accentColor"],
    required: ["videoSrc", "tagline", "taglineInSeconds"],
    size: { width: 1920, height: 1080 },
  },
  ProductReveal: {
    fields: ["productImage", "productName", "price", "tagline", "closer", "accentColor"],
    required: ["productImage", "productName", "price", "tagline", "closer"],
    size: { width: 1920, height: 1080 },
    duration: () => 8,
  },
  ProductRevealVertical: {
    fields: ["productImage", "productName", "price", "tagline", "closer", "accentColor"],
    required: ["productImage", "productName", "price", "tagline", "closer"],
    size: { width: 1080, height: 1920 },
    duration: () => 8,
  },
  CollageBurst: {
    fields: ["backgroundSrc", "backgroundInSeconds", "curtainStartSeconds", "curtainEndSeconds", "clips"],
    required: ["backgroundSrc", "curtainStartSeconds", "curtainEndSeconds", "clips"],
    size: { width: 1080, height: 1920 },
  },
  LyricOverlay: {
    fields: ["videoSrc", "lyrics", "bottomY"],
    required: ["videoSrc", "lyrics"],
    size: { width: 1080, height: 1920 },
  },
  EndTag: {
    fields: ["text", "palette", "fadeInSeconds", "holdSeconds", "fadeOutSeconds", "overlay"],
    required: ["text"],
    size: { width: 1920, height: 1080 },
    duration: (p) => ((p.fadeInSeconds as number) ?? 0.6) + ((p.holdSeconds as number) ?? 4.3) + ((p.fadeOutSeconds as number) ?? 0.6),
  },
  EndTagOverlay: {
    fields: ["text", "palette", "fadeInSeconds", "holdSeconds", "fadeOutSeconds", "overlay"],
    required: ["text"],
    size: { width: 1920, height: 1080 },
    duration: (p) => ((p.fadeInSeconds as number) ?? 1.0) + ((p.holdSeconds as number) ?? 5.69) + ((p.fadeOutSeconds as number) ?? 1.5),
  },
  CaptionOverlayOnly: {
    fields: ["words", "wordsPerPage", "fontSize", "color", "highlightColor", "backgroundColor", "fontFamily", "wordSeparator", "position"],
    required: ["words"],
    size: { width: 1920, height: 1080 },
    duration: (p) => Math.max(...(p.words as Rec[]).map((w) => w.endMs as number)) / 1000 + 0.5,
  },
  HeroTitle: {
    fields: ["title", "subtitle", "accentColor", "textColor", "subtitleColor", "scrimBackground"],
    required: ["title"],
    size: { width: 1920, height: 1080 },
    duration: () => 5,
  },
};

function checkNested(id: string, props: Rec): void {
  if (id === "CinematicRenderer") {
    array(props.scenes, "scenes").forEach((raw, i) => {
      const p = `scenes[${i}]`;
      const s = record(raw, p);
      const kind = oneOf(s.kind, ["video", "title"], `${p}.kind`);
      text(s.id, `${p}.id`);
      num(s.startSeconds, `${p}.startSeconds`, { min: 0 });
      num(s.durationSeconds, `${p}.durationSeconds`, { above: 0 });
      if (kind === "video") text(s.src, `${p}.src`);
      else text(s.text, `${p}.text`);
    });
    if (props.captions !== undefined) {
      const c = record(props.captions, "captions");
      words(c.words, "captions.words");
    }
  }
  if (id === "TalkingHead") {
    text(props.videoSrc, "videoSrc");
    words(props.captions, "captions", false);
  }
  if (id === "TitledVideo") {
    text(props.videoSrc, "videoSrc");
    text(props.tagline, "tagline");
    num(props.taglineInSeconds, "taglineInSeconds", { min: 0 });
  }
  if (id === "CollageBurst") {
    text(props.backgroundSrc, "backgroundSrc");
    array(props.clips, "clips").forEach((raw, i) => {
      const p = `clips[${i}]`;
      const c = record(raw, p);
      text(c.src, `${p}.src`);
      oneOf(c.kind, ["image", "video"], `${p}.kind`);
      const s = num(c.inSeconds, `${p}.inSeconds`, { min: 0 });
      num(c.outSeconds, `${p}.outSeconds`, { above: s });
    });
  }
  if (id === "LyricOverlay") {
    text(props.videoSrc, "videoSrc");
    array(props.lyrics, "lyrics").forEach((raw, i) => {
      const p = `lyrics[${i}]`;
      const l = record(raw, p);
      known(l, ["text", "inSeconds", "outSeconds"], p);
      text(l.text, `${p}.text`);
      const s = num(l.inSeconds, `${p}.inSeconds`, { min: 0 });
      num(l.outSeconds, `${p}.outSeconds`, { above: s });
    });
  }
  if (id === "CaptionOverlayOnly") words(props.words, "words");
  if (id === "EndTag" || id === "EndTagOverlay") {
    text(props.text, "text");
    if (props.palette !== undefined) oneOf(props.palette, ["cool_offwhite_on_black", "warm_ivory_on_black"], "palette");
  }
  if (id === "ProductReveal" || id === "ProductRevealVertical") {
    for (const key of ["productImage", "productName", "price", "tagline", "closer"]) text(props[key], key);
  }
  if (id === "HeroTitle") text(props.title, "title");
}

// validateComposition checks any composition's props and returns its frame
// shape.
export function validateComposition(id: string, input: unknown): Shape {
  if (id === "Explainer") return validateExplainer(input);
  const spec = SPECS[id];
  if (!spec) fail("composition", `must be one of ${COMPOSITION_IDS.join(", ")}`);
  const props = record(input, "props");
  known(props, [...spec.fields, ...FRAME_FIELDS], "props");
  for (const key of spec.required) {
    if (props[key] === undefined) fail(key, `is required for ${id}`);
  }
  checkNested(id, props);
  const f = frame(props, { ...spec.size, fps: DEFAULT_COMPOSITION.fps });
  let duration = f.duration;
  if (duration === undefined) {
    if (!spec.duration) fail("duration_seconds", `is required for ${id}`);
    duration = spec.duration(props);
  }
  const rounded = Math.max(1, Math.round(duration * f.fps));
  return { width: f.width, height: f.height, fps: f.fps, durationInFrames: rounded };
}
