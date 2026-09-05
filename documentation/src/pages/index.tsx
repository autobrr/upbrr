import Link from "@docusaurus/Link";
import Layout from "@theme/Layout";
import type { ReactNode } from "react";

import styles from "./index.module.css";

const features = [
  {
    title: "Prepare",
    body: "Inspect a release, collect metadata, create torrent artifacts, and generate screenshots.",
  },
  {
    title: "Review",
    body: "Check release names, tracker eligibility, duplicates, images, descriptions, and payloads.",
  },
  {
    title: "Submit",
    body: "Upload approved tracker payloads, retain registered torrents, and inject them into clients.",
  },
];

export default function Home(): ReactNode {
  return (
    <Layout
      title="Upload preparation with review built in"
      description="upbrr guides private-tracker upload preparation, review, submission, and torrent-client injection."
    >
      <main>
        <section className={styles.hero} aria-labelledby="hero-title">
          <div className={styles.heroInner}>
            <p className={styles.eyebrow}>Upload preparation workspace</p>
            <h1 id="hero-title">Review every decision before you upload.</h1>
            <p className={styles.lead}>
              upbrr brings metadata, duplicate checks, screenshots,
              descriptions, tracker payloads, and client injection into one
              guided workflow.
            </p>
            <div className={styles.actions}>
              <Link
                className="button button--primary button--lg"
                to="/docs/getting-started/quick-start"
              >
                Get started
              </Link>
              <Link
                className="button button--secondary button--lg"
                href="https://github.com/autobrr/upbrr/releases"
              >
                Download
              </Link>
            </div>
            <p className={styles.alpha}>
              Alpha software. Verify names, categories, descriptions, images,
              and torrent settings against current tracker rules before
              submission.
            </p>
          </div>
        </section>

        <section className={styles.features} aria-label="Workflow overview">
          {features.map((feature) => (
            <article className={styles.feature} key={feature.title}>
              <h2>{feature.title}</h2>
              <p>{feature.body}</p>
            </article>
          ))}
        </section>

        <section className={styles.audience}>
          <div>
            <p className={styles.eyebrow}>Before you begin</p>
            <h2>Built for experienced uploaders</h2>
          </div>
          <p>
            You should already understand your trackers' naming, category,
            media, image, and description rules. upbrr assists that work; it
            does not replace operator judgment.
          </p>
        </section>
      </main>
    </Layout>
  );
}
