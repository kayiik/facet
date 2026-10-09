import React from "react";
import { AbsoluteFill, Easing, interpolate, useCurrentFrame } from "remotion";

// The moves a cut can make into the next one. "cut" is a hard cut.
export type TransitionKind =
  | "cut"
  | "fade"
  | "slide"
  | "wipe"
  | "flip"
  | "clock-wipe"
  | "iris";

export const TRANSITION_KINDS: TransitionKind[] = [
  "cut",
  "fade",
  "slide",
  "wipe",
  "flip",
  "clock-wipe",
  "iris",
];

// TransitionFrame animates its children IN over the first `frames` frames of
// the enclosing Sequence. The outgoing cut is still rendered beneath (see the
// rule at the top of Explainer.tsx), so a fade is a true crossfade and a wipe
// reveals the new cut over the old one.
export const TransitionFrame: React.FC<{
  kind?: TransitionKind;
  frames: number;
  children: React.ReactNode;
}> = ({ kind, frames, children }) => {
  const frame = useCurrentFrame();
  if (!kind || kind === "cut" || frames <= 0 || frame >= frames) {
    return <AbsoluteFill>{children}</AbsoluteFill>;
  }
  const p = interpolate(frame, [0, frames], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: Easing.inOut(Easing.cubic),
  });
  let style: React.CSSProperties = {};
  switch (kind) {
    case "fade":
      style = { opacity: p };
      break;
    case "slide":
      style = { transform: `translateX(${(1 - p) * 100}%)` };
      break;
    case "wipe":
      style = { clipPath: `inset(0 ${(1 - p) * 100}% 0 0)` };
      break;
    case "iris":
      style = { clipPath: `circle(${p * 72}% at 50% 50%)` };
      break;
    case "clock-wipe": {
      const angle = p * 360;
      const mask = `conic-gradient(from 0deg, #000 ${angle}deg, transparent ${angle}deg)`;
      style = { maskImage: mask, WebkitMaskImage: mask };
      break;
    }
    case "flip":
      style = {
        transform: `perspective(2400px) rotateY(${(1 - p) * 90}deg)`,
        transformOrigin: "left center",
        opacity: p < 0.02 ? 0 : 1,
      };
      break;
  }
  return <AbsoluteFill style={style}>{children}</AbsoluteFill>;
};
