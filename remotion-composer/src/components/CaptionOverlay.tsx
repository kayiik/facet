import React from "react";
import {
  AbsoluteFill,
  Sequence,
  interpolate,
  spring,
  useCurrentFrame,
  useVideoConfig,
} from "remotion";

// Word-level caption for TikTok-style highlight display
export interface WordCaption {
  word: string;
  startMs: number;
  endMs: number;
  // Force a page break after this word (e.g. sentence or scene boundaries).
  // Useful for CJK captions where pages should align with clause boundaries.
  pageBreakAfter?: boolean;
}

type CaptionOverlayProps = {
  words: WordCaption[];
  // How many words to show at once in a "page"
  wordsPerPage?: number;
  fontSize?: number;
  color?: string;
  highlightColor?: string;
  backgroundColor?: string;
  fontFamily?: string;
  // Separator rendered between words. Space-delimited languages want the
  // default " "; CJK languages (no inter-word spacing) should pass "".
  wordSeparator?: string;
  position?: "bottom" | "center" | "top";
};

interface CaptionPage {
  words: WordCaption[];
  startMs: number;
  endMs: number;
}

// isLightColor reports a light #rgb or #rrggbb colour; anything else counts as
// light, which keeps the shadow.
function isLightColor(color: string): boolean {
  const hex = color.trim().replace(/^#/, "");
  const full = hex.length === 3 ? hex.split("").map((c) => c + c).join("") : hex.slice(0, 6);
  if (!/^[0-9a-fA-F]{6}$/.test(full)) return true;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16));
  return 0.299 * r + 0.587 * g + 0.114 * b > 150;
}

// Distance of the caption box from the frame's edge, as a share of its height.
export const CAPTION_EDGE = 0.05;

// buildPages splits the words at every pageBreakAfter (a sentence or a
// narration line), then splits each run into the fewest pages that hold
// wordsPerPage words, balanced: six words make two pages of three, never five
// and an orphan.
function buildPages(words: WordCaption[], wordsPerPage: number): CaptionPage[] {
  const runs: WordCaption[][] = [];
  let run: WordCaption[] = [];
  for (const w of words) {
    run.push(w);
    if (w.pageBreakAfter) {
      runs.push(run);
      run = [];
    }
  }
  if (run.length > 0) runs.push(run);
  const pages: CaptionPage[] = [];
  for (const r of runs) {
    const count = Math.ceil(r.length / wordsPerPage);
    const size = Math.ceil(r.length / count);
    for (let i = 0; i < r.length; i += size) {
      const pageWords = r.slice(i, i + size);
      pages.push({
        words: pageWords,
        startMs: pageWords[0].startMs,
        endMs: pageWords[pageWords.length - 1].endMs,
      });
    }
  }
  return pages;
}

const PageRenderer: React.FC<{
  page: CaptionPage;
  fontSize: number;
  color: string;
  highlightColor: string;
  backgroundColor: string;
  fontFamily: string;
  wordSeparator: string;
  position: "bottom" | "center" | "top";
}> = ({ page, fontSize, color, highlightColor, backgroundColor, fontFamily, wordSeparator, position }) => {
  const frame = useCurrentFrame();
  const { fps, height } = useVideoConfig();

  const currentMs = page.startMs + (frame / fps) * 1000;
  const lightText = isLightColor(color);

  // Spring entrance
  const entrance = spring({
    frame,
    fps,
    config: { damping: 18, stiffness: 120 },
  });

  const edge = Math.round(height * CAPTION_EDGE);
  return (
    <AbsoluteFill
      style={{
        justifyContent: position === "top" ? "flex-start" : position === "center" ? "center" : "flex-end",
        alignItems: "center",
        paddingBottom: position === "bottom" ? edge : 0,
        paddingTop: position === "top" ? edge : 0,
      }}
    >
      <div
        style={{
          opacity: entrance,
          transform: `translateY(${interpolate(entrance, [0, 1], [20, 0])}px)`,
          backgroundColor,
          borderRadius: 12,
          padding: "14px 28px",
          maxWidth: "80%",
          textAlign: "center",
        }}
      >
        <span
          style={{
            fontSize,
            fontWeight: 700,
            fontFamily,
            lineHeight: 1.4,
            whiteSpace: "pre-wrap",
          }}
        >
          {page.words.map((w, i) => {
            const isActive = w.startMs <= currentMs && w.endMs > currentMs;
            const isPast = w.endMs <= currentMs;
            return (
              <React.Fragment key={`${w.startMs}-${i}`}>
                <span
                  style={{
                    // Keep each word unbroken so lines wrap only at word
                    // boundaries; for CJK it prevents mid-word breaks.
                    display: "inline-block",
                    whiteSpace: "nowrap",
                    color: isActive ? highlightColor : isPast ? color : `${color}99`,
                    transition: "none", // CSS transitions forbidden in Remotion
                    // A shadow lifts light text off a dark box; dark text on a
                    // light box only blurs with one.
                    textShadow: !lightText
                      ? "none"
                      : isActive
                        ? `0 0 20px ${highlightColor}66, 0 2px 4px rgba(0,0,0,0.5)`
                        : "0 2px 4px rgba(0,0,0,0.5)",
                  }}
                >
                  {w.word}
                </span>
                {/* The separator sits outside the word's box: a space at the
                    end of an inline-block is dropped, which ran words together. */}
                {i < page.words.length - 1 ? wordSeparator : ""}
              </React.Fragment>
            );
          })}
        </span>
      </div>
    </AbsoluteFill>
  );
};

export const CaptionOverlay: React.FC<CaptionOverlayProps> = ({
  words,
  wordsPerPage = 6,
  fontSize = 42,
  color = "#F8FAFC",
  highlightColor = "#22D3EE",
  backgroundColor = "rgba(15, 23, 42, 0.75)",
  fontFamily = "Space Grotesk, Inter, system-ui, sans-serif",
  wordSeparator = " ",
  position = "bottom",
}) => {
  const { fps } = useVideoConfig();
  const pages = buildPages(words, wordsPerPage);

  return (
    <AbsoluteFill>
      {pages.map((page, i) => {
        const fromFrame = Math.round((page.startMs / 1000) * fps);
        // A page stays up until the next one starts, but not through a long
        // pause: it leaves at most 0.7 s after its last word ends.
        const nextStart = pages[i + 1]?.startMs ?? page.endMs + 700;
        const until = Math.min(nextStart, page.endMs + 700);
        const duration = Math.max(
          1,
          Math.round(((until - page.startMs) / 1000) * fps)
        );

        return (
          <Sequence key={i} from={fromFrame} durationInFrames={duration}>
            <PageRenderer
              page={page}
              fontSize={fontSize}
              color={color}
              highlightColor={highlightColor}
              backgroundColor={backgroundColor}
              fontFamily={fontFamily}
              wordSeparator={wordSeparator}
              position={position}
            />
          </Sequence>
        );
      })}
    </AbsoluteFill>
  );
};
