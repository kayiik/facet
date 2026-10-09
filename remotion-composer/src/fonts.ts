// Local fonts for offline rendering. Every family a theme or component names is
// bundled from @fontsource and loaded before the first frame renders; a font
// that fails to load fails the render instead of silently falling back.
import "@fontsource/inter/300.css";
import "@fontsource/inter/400.css";
import "@fontsource/inter/500.css";
import "@fontsource/inter/600.css";
import "@fontsource/inter/700.css";
import "@fontsource/inter/800.css";
import "@fontsource/space-grotesk/300.css";
import "@fontsource/space-grotesk/400.css";
import "@fontsource/space-grotesk/500.css";
import "@fontsource/space-grotesk/700.css";
import "@fontsource/playfair-display/400.css";
import "@fontsource/playfair-display/400-italic.css";
import "@fontsource/playfair-display/700.css";
import "@fontsource/playfair-display/700-italic.css";
import "@fontsource/playfair-display/900.css";
import "@fontsource/noto-sans/300.css";
import "@fontsource/noto-sans/400.css";
import "@fontsource/noto-sans/700.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/700.css";
import { cancelRender, continueRender, delayRender } from "remotion";

export const FONT_FAMILIES = [
  "Inter",
  "Space Grotesk",
  "Playfair Display",
  "Noto Sans",
  "JetBrains Mono",
];

const FACES = [
  "300 16px Inter",
  "400 16px Inter",
  "500 16px Inter",
  "600 16px Inter",
  "700 16px Inter",
  "800 16px Inter",
  '300 16px "Space Grotesk"',
  '400 16px "Space Grotesk"',
  '500 16px "Space Grotesk"',
  '700 16px "Space Grotesk"',
  '400 16px "Playfair Display"',
  'italic 400 16px "Playfair Display"',
  '700 16px "Playfair Display"',
  'italic 700 16px "Playfair Display"',
  '900 16px "Playfair Display"',
  '300 16px "Noto Sans"',
  '400 16px "Noto Sans"',
  '700 16px "Noto Sans"',
  '400 16px "JetBrains Mono"',
  '700 16px "JetBrains Mono"',
];

let started = false;

// ensureFonts blocks rendering until every bundled face is loaded. Call it
// while rendering the root (see Root.tsx), never as this module loads.
export function ensureFonts(): void {
  if (started || typeof document === "undefined" || !document.fonts) {
    return;
  }
  started = true;
  const handle = delayRender("Loading bundled fonts");
  Promise.all(FACES.map((face) => document.fonts.load(face)))
    .then((loaded) => {
      const missing = FACES.filter((_, i) => loaded[i].length === 0);
      if (missing.length > 0) {
        cancelRender(new Error(`bundled fonts did not load: ${missing.join(", ")}`));
        return;
      }
      continueRender(handle);
    })
    .catch((err) => cancelRender(err));
}

