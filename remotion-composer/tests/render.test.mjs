import assert from "node:assert/strict";
import {mkdtemp, mkdir, rm, writeFile} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {spawnSync} from "node:child_process";
import test from "node:test";
import {fileURLToPath} from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const cli = path.join(root, "node_modules", "@remotion", "cli", "remotion-cli.js");
const entry = path.join(root, "src", "index.tsx");

const run = (command, args, options = {}) => {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: options.encoding ?? "utf8",
    maxBuffer: 20 * 1024 * 1024,
  });
  assert.equal(
    result.status,
    0,
    `${command} ${args.join(" ")}\n${result.stdout ?? ""}\n${result.stderr ?? ""}`,
  );
  return result.stdout;
};

const render = async (t, props, {muted = false, publicDir} = {}) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "facet-composer-test-"));
  t.after(() => rm(directory, {recursive: true, force: true}));
  const propsPath = path.join(directory, "props.json");
  const output = path.join(directory, "output.mp4");
  await writeFile(propsPath, JSON.stringify(props));
  const args = [
    cli,
    "render",
    entry,
    "Explainer",
    output,
    `--props=${propsPath}`,
    "--codec=h264",
    "--log=error",
  ];
  if (publicDir) args.push(`--public-dir=${publicDir}`);
  if (muted) args.push("--muted");
  run(process.execPath, args);
  return output;
};

const duration = (file) =>
  Number(
    run("ffprobe", [
      "-v",
      "error",
      "-select_streams",
      "v:0",
      "-show_entries",
      "stream=duration",
      "-of",
      "default=nw=1:nk=1",
      file,
    ]).trim(),
  );

const grayPixel = (file, seconds) => {
  const result = spawnSync(
    "ffmpeg",
    [
      "-v",
      "error",
      "-ss",
      String(seconds),
      "-i",
      file,
      "-frames:v",
      "1",
      "-vf",
      "scale=1:1,format=gray",
      "-f",
      "rawvideo",
      "pipe:1",
    ],
    {encoding: null},
  );
  assert.equal(result.status, 0, result.stderr?.toString());
  return result.stdout[0];
};

test("renders direct cuts with visible changing content and exact duration", async (t) => {
  const output = await render(
    t,
    {
      width: 320,
      height: 180,
      fps: 10,
      duration_seconds: 1,
      cuts: [
        {
          id: "first",
          type: "text_card",
          text: "First",
          backgroundColor: "#7f1d1d",
          in_seconds: 0,
          out_seconds: 0.5,
        },
        {
          id: "second",
          type: "text_card",
          text: "Second",
          backgroundColor: "#1d4ed8",
          in_seconds: 0.5,
          out_seconds: 1,
        },
      ],
    },
    {muted: true},
  );
  assert.ok(Math.abs(duration(output) - 1) < 0.01);
  const first = grayPixel(output, 0.25);
  const second = grayPixel(output, 0.75);
  assert.ok(first > 8, `first frame is blank: ${first}`);
  assert.ok(second > 8, `second frame is blank: ${second}`);
  assert.notEqual(first, second, "direct cut boundary did not change the frame");
});

test("silent graphics have no audio stream", async (t) => {
  const output = await render(
    t,
    {
      width: 320,
      height: 180,
      fps: 10,
      duration_seconds: 0.5,
      cuts: [
        {
          type: "hero_title",
          text: "Silent",
          in_seconds: 0,
          out_seconds: 0.5,
        },
      ],
    },
    {muted: true},
  );
  const streams = run("ffprobe", [
    "-v",
    "error",
    "-select_streams",
    "a",
    "-show_entries",
    "stream=index",
    "-of",
    "csv=p=0",
    output,
  ]).trim();
  assert.equal(streams, "");
});

test("local staged narration and music produce audio without extending video", async (t) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "facet-composer-audio-"));
  t.after(() => rm(directory, {recursive: true, force: true}));
  const publicDir = path.join(directory, "public");
  await mkdir(publicDir);
  const tone = path.join(publicDir, "tone.wav");
  run("ffmpeg", [
    "-v",
    "error",
    "-y",
    "-f",
    "lavfi",
    "-i",
    "sine=frequency=440:duration=1",
    "-c:a",
    "pcm_s16le",
    tone,
  ]);
  const music = path.join(publicDir, "music.wav");
  run("ffmpeg", [
    "-v",
    "error",
    "-y",
    "-f",
    "lavfi",
    "-i",
    "sine=frequency=220:duration=0.2",
    "-c:a",
    "pcm_s16le",
    music,
  ]);
  const output = await render(
    t,
    {
      width: 320,
      height: 180,
      fps: 10,
      duration_seconds: 0.5,
      cuts: [
        {
          type: "stat_card",
          stat: "1",
          label: "audio track",
          in_seconds: 0,
          out_seconds: 0.5,
        },
      ],
      audio: {
        narration: {src: "tone.wav", volume: 0.5},
        music: {src: "music.wav", volume: 0.1, loop: true},
      },
    },
    {publicDir},
  );
  const codec = run("ffprobe", [
    "-v",
    "error",
    "-select_streams",
    "a:0",
    "-show_entries",
    "stream=codec_name",
    "-of",
    "default=nw=1:nk=1",
    output,
  ]).trim();
  assert.equal(codec, "aac");
  assert.ok(Math.abs(duration(output) - 0.5) < 0.01);
});
