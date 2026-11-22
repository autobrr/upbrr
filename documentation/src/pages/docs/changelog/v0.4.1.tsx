import Link from "@docusaurus/Link";
import { Redirect } from "@docusaurus/router";
import useBaseUrl from "@docusaurus/useBaseUrl";
import type { ReactNode } from "react";

export default function PreviousReleaseRedirect(): ReactNode {
  const target = useBaseUrl("/docs/getting-started/upgrading");
  return (
    <>
      <Redirect to={target} />
      <p>
        <Link to={target}>Read current upgrade guidance.</Link>
      </p>
    </>
  );
}
