import {
  AbsoluteFill,
  interpolate,
  spring,
  useCurrentFrame,
  useVideoConfig,
} from "remotion";

type HeroTitleProps = {
  title: string;
  subtitle?: string;
  /** Color of the first word and the underline. */
  accentColor?: string;
  /** Color of the remaining words. Pass the theme's textColor. */
  textColor?: string;
  /** Subtitle color. */
  subtitleColor?: string;
  /**
   * Scrim painted behind the title so it separates from whatever is underneath.
   * Defaults to a dark wash; a light theme must pass a light one, otherwise the
   * scrim darkens the backdrop and cancels out the theme's dark text.
   */
  scrimBackground?: string;
};

const DEFAULT_SCRIM =
  "radial-gradient(ellipse at center, rgba(15,23,42,0.35) 0%, rgba(15,23,42,0.55) 100%)";

export const HeroTitle: React.FC<HeroTitleProps> = ({
  title,
  subtitle,
  accentColor = "#22D3EE",
  textColor = "#F8FAFC",
  subtitleColor = "#A78BFA",
  scrimBackground = DEFAULT_SCRIM,
}) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();

  // Staggered letter-by-letter spring, laid out word by word so a line wraps
  // only between words. The first word carries the accent.
  const words = title.split(/\s+/).filter(Boolean);
  const titleChars = words.join(" ").split("");
  const titleSize = titleChars.length <= 28 ? 112 : titleChars.length <= 48 ? 92 : 72;

  return (
    <AbsoluteFill
      style={{
        justifyContent: "center",
        alignItems: "center",
        background: scrimBackground,
      }}
    >
      <div style={{ textAlign: "center", maxWidth: "85%" }}>
        {/* Main title with per-character spring */}
        <div
          style={{
            fontSize: titleSize,
            fontWeight: 800,
            fontFamily: "Space Grotesk, Inter, system-ui, sans-serif",
            lineHeight: 1.15,
            display: "flex",
            justifyContent: "center",
            flexWrap: "wrap",
            columnGap: "0.28em",
          }}
        >
          {words.map((word, w) => {
            const offset = words.slice(0, w).reduce((n, x) => n + x.length + 1, 0);
            return (
              <span key={w} style={{ display: "inline-block", whiteSpace: "nowrap" }}>
                {word.split("").map((char, c) => {
                  const charSpring = spring({
                    frame: frame - (offset + c) * 1.2,
                    fps,
                    config: { damping: 12, stiffness: 150 },
                  });
                  return (
                    <span
                      key={c}
                      style={{
                        display: "inline-block",
                        opacity: charSpring,
                        transform: `translateY(${interpolate(charSpring, [0, 1], [30, 0])}px)`,
                        color: w === 0 ? accentColor : textColor,
                      }}
                    >
                      {char}
                    </span>
                  );
                })}
              </span>
            );
          })}
        </div>

        {/* Subtitle */}
        {subtitle && (
          <div
            style={{
              marginTop: 20,
              opacity: spring({
                frame: frame - titleChars.length * 1.2 - 5,
                fps,
                config: { damping: 20 },
              }),
              fontSize: 36,
              fontWeight: 500,
              color: subtitleColor,
              fontFamily: "Space Grotesk, Inter, system-ui, sans-serif",
              letterSpacing: "0.1em",
              textTransform: "uppercase",
            }}
          >
            {subtitle}
          </div>
        )}

        {/* Animated underline */}
        <div
          style={{
            margin: "24px auto 0",
            height: 3,
            backgroundColor: accentColor,
            borderRadius: 2,
            width: interpolate(
              spring({
                frame: frame - 15,
                fps,
                config: { damping: 15, stiffness: 60 },
              }),
              [0, 1],
              [0, 400]
            ),
          }}
        />
      </div>
    </AbsoluteFill>
  );
};
