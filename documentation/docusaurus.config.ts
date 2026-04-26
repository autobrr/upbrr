import type * as Preset from "@docusaurus/preset-classic";
import type { Config } from "@docusaurus/types";
import type { PrismTheme } from "prism-react-renderer";

const minimalLightTheme: PrismTheme = {
  plain: { color: "#172033", backgroundColor: "#f7f8fb" },
  styles: [
    {
      types: ["comment", "prolog", "doctype", "cdata"],
      style: { color: "#687386" },
    },
    { types: ["punctuation"], style: { color: "#4b5563" } },
    {
      types: ["property", "tag", "boolean", "number", "constant", "symbol"],
      style: { color: "#047857" },
    },
    {
      types: ["selector", "attr-name", "string", "char", "builtin"],
      style: { color: "#2563eb" },
    },
    { types: ["operator", "entity", "url"], style: { color: "#4b5563" } },
    { types: ["atrule", "attr-value", "keyword"], style: { color: "#7c3aed" } },
    { types: ["function", "class-name"], style: { color: "#b91c1c" } },
    { types: ["regex", "important", "variable"], style: { color: "#c2410c" } },
  ],
};

const minimalDarkTheme: PrismTheme = {
  plain: { color: "#e5e7eb", backgroundColor: "#151924" },
  styles: [
    {
      types: ["comment", "prolog", "doctype", "cdata"],
      style: { color: "#8b95a7" },
    },
    { types: ["punctuation"], style: { color: "#cbd5e1" } },
    {
      types: ["property", "tag", "boolean", "number", "constant", "symbol"],
      style: { color: "#5eead4" },
    },
    {
      types: ["selector", "attr-name", "string", "char", "builtin"],
      style: { color: "#93c5fd" },
    },
    { types: ["operator", "entity", "url"], style: { color: "#cbd5e1" } },
    { types: ["atrule", "attr-value", "keyword"], style: { color: "#c4b5fd" } },
    { types: ["function", "class-name"], style: { color: "#fca5a5" } },
    { types: ["regex", "important", "variable"], style: { color: "#fdba74" } },
  ],
};

const config: Config = {
  title: "upbrr",
  tagline:
    "Upload preparation and tracker submission for private-tracker workflows",
  favicon: "img/favicon.ico",
  url: "https://upbrr.com",
  baseUrl: "/",
  organizationName: "autobrr",
  projectName: "upbrr",
  onBrokenLinks: "throw",
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: "warn",
    },
  },
  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },
  themes: [
    [
      require.resolve("@easyops-cn/docusaurus-search-local"),
      {
        hashed: true,
        docsRouteBasePath: "/docs",
        language: "en",
        docsDir: "docs",
        searchBarShortcutHint: false,
      },
    ],
  ],
  plugins: [
    [
      "docusaurus-plugin-llms",
      {
        docsDir: "docs",
        generateLLMsTxt: true,
        generateLLMsFullTxt: true,
        includeBlog: false,
        excludeImports: true,
        removeDuplicateHeadings: true,
      },
    ],
  ],
  presets: [
    [
      "classic",
      {
        docs: {
          sidebarPath: "./sidebars.ts",
          editUrl: "https://github.com/autobrr/upbrr/tree/main/documentation/",
          routeBasePath: "docs",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],
  themeConfig: {
    image: "img/social-card.svg",
    colorMode: {
      defaultMode: "dark",
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: "upbrr",
      logo: {
        alt: "upbrr Logo",
        src: "img/icon-192.png",
      },
      items: [
        {
          type: "docSidebar",
          sidebarId: "docsSidebar",
          position: "left",
          label: "Docs",
        },
        {
          href: "https://discord.autobrr.com",
          position: "right",
          label: "Discord",
          className: "navbar-icon-link navbar-icon-link--discord",
        },
        {
          href: "https://github.com/autobrr/upbrr",
          position: "right",
          label: "GitHub",
          className: "navbar-icon-link navbar-icon-link--github",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [
        {
          title: "Docs",
          items: [
            { label: "Quick Start", to: "/docs/getting-started/quick-start" },
            { label: "CLI Usage", to: "/docs/usage/cli" },
            { label: "Configuration", to: "/docs/configuration/overview" },
          ],
        },
        {
          title: "Workflows",
          items: [
            { label: "Upload Workflow", to: "/docs/workflows/upload-workflow" },
            { label: "Screenshots", to: "/docs/workflows/screenshots" },
            { label: "Tracker Uploads", to: "/docs/workflows/tracker-upload" },
          ],
        },
        {
          title: "Project",
          items: [
            {
              label: "GitHub",
              href: "https://github.com/autobrr/upbrr",
              className: "footer-icon-link footer-icon-link--github",
            },
            {
              label: "Issues",
              href: "https://github.com/autobrr/upbrr/issues",
            },
            { label: "llms.txt", href: "https://upbrr.com/llms.txt" },
            { label: "llms-full.txt", href: "https://upbrr.com/llms-full.txt" },
          ],
        },
      ],
      copyright: `Copyright ${new Date().getFullYear()} autobrr`,
    },
    prism: {
      theme: minimalLightTheme,
      darkTheme: minimalDarkTheme,
      additionalLanguages: ["bash", "powershell", "yaml", "json", "go"],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
