import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    "introduction",
    {
      type: "category",
      label: "Getting started",
      items: [
        "getting-started/quick-start",
        "getting-started/installation",
        "getting-started/upgrading",
        "getting-started/migrate-from-upload-assistant",
      ],
    },
    {
      type: "category",
      label: "Configuration",
      items: ["configuration/index", "configuration/web-server"],
    },
    "cli/index",
    {
      type: "category",
      label: "Web UI",
      link: { type: "doc", id: "web-ui/index" },
      items: [
        {
          type: "category",
          label: "Settings",
          link: { type: "doc", id: "web-ui/settings/index" },
          items: [
            "web-ui/settings/main",
            "web-ui/settings/image-hosting",
            "web-ui/settings/metadata",
            "web-ui/settings/screens",
            "web-ui/settings/description",
            "web-ui/settings/arr",
            "web-ui/settings/post-upload",
            "web-ui/settings/trackers",
            "web-ui/settings/torrent-clients",
            "web-ui/settings/client-handling",
            "web-ui/settings/torrent-specific",
            "web-ui/settings/application-details",
            "web-ui/settings/api-tokens",
            "web-ui/settings/tracker-auth",
          ],
        },
        "web-ui/logging",
      ],
    },
    "workflow/index",
    "trackers/index",
    "troubleshooting/index",
    "api/index",
    "contributing",
  ],
};

export default sidebars;
