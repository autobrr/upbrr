import type { KnipConfig } from "knip";

const config: KnipConfig = {
  project: ["src/**/*.{ts,tsx,css}"],
  compilers: {
    ".css": (_filename, contents) => contents,
  },
};

export default config;
