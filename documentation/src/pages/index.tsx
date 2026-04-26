import clsx from "clsx";
import type { ReactElement } from "react";
import Heading from "@theme/Heading";
import Layout from "@theme/Layout";
import Link from "@docusaurus/Link";
import styles from "./index.module.css";

const features = [
  {
    title: "Shared upload core",
    body: "Prepare metadata, descriptions, screenshots, torrents, and tracker payloads through the same backend used by CLI, GUI, and web mode.",
  },
  {
    title: "Safety-first execution",
    body: "Use dry-run, site-check, unattended, and upload-only flows to keep automation explicit and conservative.",
  },
  {
    title: "Private-tracker workflow",
    body: "Coordinate dupe checks, rule gates, image hosting, and tracker-specific upload behavior from one local tool.",
  },
];

export default function Home(): ReactElement {
  return (
    <Layout
      title="upbrr documentation"
      description="Documentation for upbrr upload preparation and tracker submission"
    >
      <header className={clsx("hero hero--upbrr", styles.hero)}>
        <div className={styles.heroInner}>
          <p className={styles.kicker}>autobrr project</p>
          <Heading as="h1" className={styles.title}>
            upbrr
          </Heading>
          <p className={styles.subtitle}>
            Upload preparation and tracker submission for private-tracker
            workflows, available as a CLI, Wails desktop app, and embedded web
            interface.
          </p>
          <div className={styles.actions}>
            <Link
              className="button button--primary button--lg"
              to="/docs/getting-started/quick-start"
            >
              Quick start
            </Link>
            <Link
              className="button button--secondary button--lg"
              to="/docs/workflows/upload-workflow"
            >
              Upload workflow
            </Link>
          </div>
        </div>
      </header>
      <main>
        <section className={styles.features}>
          <div className="container feature-grid">
            {features.map((feature) => (
              <article className="feature-panel" key={feature.title}>
                <Heading as="h3">{feature.title}</Heading>
                <p>{feature.body}</p>
              </article>
            ))}
          </div>
        </section>
      </main>
    </Layout>
  );
}
