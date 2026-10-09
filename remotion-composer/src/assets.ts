import {staticFile} from "remotion";

export const resolveAsset = (source: string): string => {
  if (
    source.startsWith("http://") ||
    source.startsWith("https://") ||
    source.startsWith("data:")
  ) {
    return source;
  }
  return staticFile(source);
};
