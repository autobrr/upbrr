import type { KnipConfig } from "knip";

const config: KnipConfig = {
  // Knip does not follow CSS @import from styles.css.
  entry: ["vitest.config.ts", "src/test/setup.ts", "src/components.css"],
  project: ["src/**/*.{ts,tsx,css}", "vitest.config.ts"],
  ignore: ["src/api/generated/**"],
  // Knip does not resolve CSS @import packages; styles.css imports Tailwind directly.
  ignoreDependencies: ["tailwindcss"],
  compilers: {
    ".css": (_filename, contents) => contents,
  },
};

export default config;
