import type { KnipConfig } from "knip";

const config: KnipConfig = {
  entry: ["vitest.config.ts", "src/test/setup.ts"],
  project: ["src/**/*.{ts,tsx,css}", "vitest.config.ts"],
  compilers: {
    ".css": (_filename, contents) => contents,
  },
};

export default config;
