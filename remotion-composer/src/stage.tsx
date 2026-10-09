import React from "react";
import { AbsoluteFill, useVideoConfig } from "remotion";

// Stage lays out designed-at-fixed-size scenes (cards, charts, titles) on a
// 1920x1080 canvas for landscape output or a 1080x1920 canvas for portrait,
// scaled to fit the real frame and centred. Media scenes don't use it: they
// fill the frame themselves.
//
// reserveTop and reserveBottom (frame pixels) keep a band free, so a scene is
// laid out above the captions instead of under them.
export const Stage: React.FC<{ children: React.ReactNode; reserveTop?: number; reserveBottom?: number }> = ({
  children,
  reserveTop = 0,
  reserveBottom = 0,
}) => {
  const { width, height } = useVideoConfig();
  const portrait = height > width;
  const baseW = portrait ? 1080 : 1920;
  const baseH = portrait ? 1920 : 1080;
  const available = Math.max(1, height - reserveTop - reserveBottom);
  const scale = Math.min(width / baseW, available / baseH);
  const left = (width - baseW * scale) / 2;
  const top = reserveTop + (available - baseH * scale) / 2;
  return (
    <AbsoluteFill>
      <div
        style={{
          position: "absolute",
          left,
          top,
          width: baseW,
          height: baseH,
          transform: `scale(${scale})`,
          transformOrigin: "top left",
        }}
      >
        {children}
      </div>
    </AbsoluteFill>
  );
};