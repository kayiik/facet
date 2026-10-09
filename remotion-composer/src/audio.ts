// Music ducking: lower the music while narration speaks.
//
// `ranges` are the narration's speaking spans in seconds (Facet fills them from
// the narration timing record). Inside a range the music plays at `level` times
// its volume; it ramps down over `attackSeconds` before a range and back up
// over `releaseSeconds` after it.

export interface DuckConfig {
  ranges: [number, number][];
  level?: number;
  attackSeconds?: number;
  releaseSeconds?: number;
}

export function duckMultiplier(seconds: number, duck: DuckConfig | undefined): number {
  if (!duck || duck.ranges.length === 0) {
    return 1;
  }
  const level = duck.level ?? 0.25;
  const attack = duck.attackSeconds ?? 0.15;
  const release = duck.releaseSeconds ?? 0.4;
  let factor = 1;
  for (const [start, end] of duck.ranges) {
    let f = 1;
    if (seconds >= start && seconds <= end) {
      f = level;
    } else if (seconds < start && seconds >= start - attack && attack > 0) {
      f = 1 - ((seconds - (start - attack)) / attack) * (1 - level);
    } else if (seconds > end && seconds <= end + release && release > 0) {
      f = level + ((seconds - end) / release) * (1 - level);
    }
    factor = Math.min(factor, f);
  }
  return factor;
}
