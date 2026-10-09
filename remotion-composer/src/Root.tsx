import React from "react";
import { CalculateMetadataFunction, Composition } from "remotion";

import { ensureFonts } from "./fonts";
import { validateComposition } from "./contract";
import { Explainer } from "./Explainer";
import { CinematicRenderer } from "./CinematicRenderer";
import { TalkingHead } from "./TalkingHead";
import { TitledVideo } from "./TitledVideo";
import { CollageBurst } from "./CollageBurst";
import { LyricOverlay } from "./LyricOverlay";
import { EndTag } from "./components/EndTag";
import { HeroTitle } from "./components/HeroTitle";
import { ProductReveal } from "./components/ProductReveal";
import { CaptionOverlay } from "./components/CaptionOverlay";

type AnyProps = Record<string, unknown>;

// Every composition validates its props before rendering and takes its size,
// frame rate and length from them.
const metadataFor =
  (id: string): CalculateMetadataFunction<AnyProps> =>
  ({ props }) => {
    const shape = validateComposition(id, props);
    return {
      width: shape.width,
      height: shape.height,
      fps: shape.fps,
      durationInFrames: shape.durationInFrames,
    };
  };

const compositions: { id: string; component: React.FC<any>; defaultProps: AnyProps; width: number; height: number }[] = [
  {
    id: "Explainer",
    component: Explainer,
    width: 1920,
    height: 1080,
    defaultProps: {
      cuts: [{ id: "intro", type: "text_card", text: "Facet Explainer", in_seconds: 0, out_seconds: 2 }],
    },
  },
  {
    id: "CinematicRenderer",
    component: CinematicRenderer,
    width: 1920,
    height: 1080,
    defaultProps: { scenes: [{ id: "title", kind: "title", text: "Facet", startSeconds: 0, durationSeconds: 2 }] },
  },
  {
    id: "TalkingHead",
    component: TalkingHead,
    width: 1080,
    height: 1920,
    defaultProps: { videoSrc: "sample.mp4", captions: [], duration_seconds: 2 },
  },
  {
    id: "TitledVideo",
    component: TitledVideo,
    width: 1920,
    height: 1080,
    defaultProps: { videoSrc: "sample.mp4", tagline: "Facet", taglineInSeconds: 0, duration_seconds: 2 },
  },
  {
    id: "ProductReveal",
    component: ProductReveal,
    width: 1920,
    height: 1080,
    defaultProps: { productImage: "product.png", productName: "Product", price: "Price", tagline: "Tagline", closer: "Closer" },
  },
  {
    id: "ProductRevealVertical",
    component: ProductReveal,
    width: 1080,
    height: 1920,
    defaultProps: { productImage: "product.png", productName: "Product", price: "Price", tagline: "Tagline", closer: "Closer" },
  },
  {
    id: "CollageBurst",
    component: CollageBurst,
    width: 1080,
    height: 1920,
    defaultProps: { backgroundSrc: "background.mp4", curtainStartSeconds: 1.5, curtainEndSeconds: 3, clips: [], duration_seconds: 4 },
  },
  {
    id: "LyricOverlay",
    component: LyricOverlay,
    width: 1080,
    height: 1920,
    defaultProps: { videoSrc: "sample.mp4", lyrics: [], duration_seconds: 2 },
  },
  { id: "EndTag", component: EndTag, width: 1920, height: 1080, defaultProps: { text: "FACET" } },
  { id: "EndTagOverlay", component: EndTag, width: 1920, height: 1080, defaultProps: { text: "FACET", overlay: true } },
  {
    id: "CaptionOverlayOnly",
    component: CaptionOverlay,
    width: 1920,
    height: 1080,
    defaultProps: { words: [{ word: "Facet", startMs: 0, endMs: 1000 }] },
  },
  { id: "HeroTitle", component: HeroTitle, width: 1920, height: 1080, defaultProps: { title: "Facet" } },
];

export const Root: React.FC = () => {
  // Fonts block the first frame. The hold starts here, inside the tree, not as
  // the module loads: Remotion resets its list of holds while its own modules
  // load, and a hold dropped from that list still times out the render.
  React.useState(ensureFonts);
  return (
    <>
      {compositions.map((c) => (
        <Composition
          key={c.id}
          id={c.id}
          component={c.component}
          width={c.width}
          height={c.height}
          fps={30}
          durationInFrames={60}
          defaultProps={c.defaultProps}
          calculateMetadata={metadataFor(c.id)}
        />
      ))}
    </>
  );
};