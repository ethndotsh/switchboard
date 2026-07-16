import { defineConfig } from "blume";

export default defineConfig({
  title: "Switchboard",
  description:
    "Programmable reverse proxy rules, written in Go, compiled to WebAssembly, and hot-swapped without restarts.",

  logo: {
    image: "/logo.svg",
    text: "",
  },

  content: {
    sources: [
      { type: "filesystem", root: "content" },
      {
        type: "github-releases",
        prefix: "changelog",
        owner: "ethndotsh",
        repo: "switchboard",
      },
    ],
  },

  navigation: {
    tabs: [
      { label: "Docs", path: "/", icon: "book-open" },
      { label: "Changelog", path: "/changelog", icon: "history" },
    ],
  },

  github: {
    owner: "ethndotsh",
    repo: "switchboard",
    branch: "master",
    dir: "docs",
  },

  theme: {
    accent: "#6E79D6",
    mode: "system",
  },

  lastModified: true,

  seo: {
    sitemap: true,
    robots: true,
    structuredData: true,
  },

  deployment: {
    output: "static",
  },
});
