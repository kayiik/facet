import {
  AbsoluteFill,
  interpolate,
  spring,
  useCurrentFrame,
  useVideoConfig,
} from "remotion";

interface SectionTitleProps {
  title: string;
  subtitle?: string;
  accentColor?: string;
  /** Title color. Defaults to near-white; pass the theme's textColor on light themes. */
  textColor?: string;
  position?: "top-left" | "bottom-left" | "center";
  /** Size multiplier: 1 is a corner label over footage; a full-frame card uses about 2.4. */
  scale?: number;
  /** A shadow lifts the label off footage; a flat card needs none. */
  shadow?: boolean;
}

export const SectionTitle: React.FC<SectionTitleProps> = ({
  title,
  subtitle,
  accentColor = "#22D3EE",
  textColor = "#F8FAFC",
  position = "top-left",
  scale = 1,
  shadow = true,
}) => {
  const frame = useCurrentFrame();
  const { fps, durationInFrames } = useVideoConfig();

  // Entrance spring
  const slideIn = spring({
    frame,
    fps,
    config: { damping: 15, stiffness: 80 },
  });

  // Exit fade
  const exitStart = durationInFrames - 15;
  const fadeOut = interpolate(frame, [exitStart, durationInFrames], [1, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  const opacity = Math.min(slideIn, fadeOut);

  const positionStyles: React.CSSProperties =
    position === "center"
      ? { justifyContent: "center", alignItems: "center" }
      : position === "bottom-left"
      ? { justifyContent: "flex-end", alignItems: "flex-start", padding: 60 }
      : { justifyContent: "flex-start", alignItems: "flex-start", padding: 60 };
  const textShadow = shadow ? "0 2px 8px rgba(0,0,0,0.6)" : undefined;

  return (
    <AbsoluteFill style={positionStyles}>
      <div
        style={{
          opacity,
          maxWidth: "85%",
          transform: `translateX(${interpolate(slideIn, [0, 1], [-40 * scale, 0])}px)`,
        }}
      >
        {/* Accent bar */}
        <div
          style={{
            width: interpolate(slideIn, [0, 1], [0, 60 * scale]),
            height: Math.round(4 * Math.min(scale, 2)),
            backgroundColor: accentColor,
            marginBottom: 12 * scale,
            borderRadius: 2,
          }}
        />
        <div
          style={{
            fontSize: Math.round(28 * scale),
            fontWeight: 700,
            color: textColor,
            fontFamily: "Space Grotesk, Inter, system-ui, sans-serif",
            letterSpacing: "0.05em",
            textTransform: "uppercase",
            textShadow,
          }}
        >
          {title}
        </div>
        {subtitle && (
          <div
            style={{
              fontSize: Math.round(18 * scale),
              fontWeight: 400,
              color: accentColor,
              fontFamily: "Space Grotesk, Inter, system-ui, sans-serif",
              marginTop: 4 * scale,
              opacity: spring({
                frame: frame - 8,
                fps,
                config: { damping: 20 },
              }),
              textShadow,
            }}
          >
            {subtitle}
          </div>
        )}
      </div>
    </AbsoluteFill>
  );
};
