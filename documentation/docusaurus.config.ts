import type * as Preset from "@docusaurus/preset-classic";
import type { Config } from "@docusaurus/types";
import { themes } from "prism-react-renderer";

const config: Config = {
  title: "upbrr",
  tagline: "Prepare, review, and submit private-tracker uploads",
  favicon: "img/favicon.ico",

  url: "https://upbrr.com",
  baseUrl: "/",
  organizationName: "autobrr",
  projectName: "upbrr",

  onBrokenLinks: "throw",
  onDuplicateRoutes: "throw",
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: "throw",
    },
  },

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  themes: [
    [
      "@easyops-cn/docusaurus-search-local",
      {
        hashed: true,
        docsRouteBasePath: "/docs",
        docsDir: "docs",
        indexBlog: false,
        indexPages: true,
        language: "en",
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
          routeBasePath: "docs",
          editUrl: "https://github.com/autobrr/upbrr/edit/main/documentation/",
          showLastUpdateTime: false,
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
        sitemap: {
          changefreq: "weekly",
          priority: 0.5,
          filename: "sitemap.xml",
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: "img/icon-512.png",
    metadata: [
      {
        name: "keywords",
        content:
          "upbrr, upload assistant, private tracker, torrent upload preparation",
      },
    ],
    colorMode: {
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: "upbrr",
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
          "aria-label": "autobrr Discord",
        },
        {
          href: "https://github.com/autobrr/upbrr",
          position: "right",
          label: "GitHub",
          "aria-label": "upbrr GitHub repository",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [
        {
          title: "Docs",
          items: [
            { label: "Quick start", to: "/docs/getting-started/quick-start" },
            { label: "Configuration", to: "/docs/configuration" },
            { label: "Troubleshooting", to: "/docs/troubleshooting" },
          ],
        },
        {
          title: "Project",
          items: [
            { label: "GitHub", href: "https://github.com/autobrr/upbrr" },
            {
              label: "Releases",
              href: "https://github.com/autobrr/upbrr/releases",
            },
            {
              label: "Issues",
              href: "https://github.com/autobrr/upbrr/issues",
            },
          ],
        },
      ],
      copyright: "Copyright 2026 autobrr",
    },
    prism: {
      theme: themes.github,
      darkTheme: themes.dracula,
      additionalLanguages: [
        "bash",
        "docker",
        "json",
        "nginx",
        "powershell",
        "yaml",
      ],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
