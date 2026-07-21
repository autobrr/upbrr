// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FormEvent } from "react";
import { useEffect, useState } from "react";
import App from "./app";
import { Checkbox } from "./components/ui/checkbox";
import {
  browserAuth,
  initializeBrowserBridge,
  isBrowserMode,
  updateBrowserCSRFToken,
  withBrowserBasePath,
} from "./utils/runtime";

type AuthStatus = {
  authenticated: boolean;
  needsSetup: boolean;
  username: string;
  csrfToken: string;
  nativeBrowseEnabled: boolean;
  caseInsensitivePaths: boolean;
  browseRoot: string;
  allowUnrestrictedBrowse: boolean;
  needsBrowsePolicy: boolean;
  oidcEnabled: boolean;
  oidcDisableBuiltInLogin: boolean;
};

const initialStatus: AuthStatus = {
  authenticated: false,
  needsSetup: false,
  username: "",
  csrfToken: "",
  nativeBrowseEnabled: false,
  caseInsensitivePaths: false,
  browseRoot: "",
  allowUnrestrictedBrowse: false,
  needsBrowsePolicy: false,
  oidcEnabled: false,
  oidcDisableBuiltInLogin: false,
};

/** Reason codes the OIDC callback appends when it sends the browser back. */
const oidcErrorMessages: Record<string, string> = {
  oidc_denied: "The identity provider denied the sign-in request.",
  oidc_failed: "Single sign-on failed. Check the server log for details.",
  oidc_unavailable: "The identity provider is unreachable. Try again shortly.",
};

/**
 * readOIDCError consumes the ?oidc_error= code left by a failed callback and
 * strips it from the URL, so a refresh does not redisplay a stale failure.
 */
const readOIDCError = (): string => {
  const params = new URLSearchParams(window.location.search);
  const code = params.get("oidc_error");
  if (!code) {
    return "";
  }
  params.delete("oidc_error");
  const query = params.toString();
  window.history.replaceState(
    null,
    "",
    `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`,
  );
  return oidcErrorMessages[code] || oidcErrorMessages.oidc_failed;
};

export default function WebRoot() {
  const browserMode = isBrowserMode();
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [retainLogin, setRetainLogin] = useState(false);
  const [browseRoot, setBrowseRoot] = useState("");
  const [allowUnrestrictedBrowse, setAllowUnrestrictedBrowse] = useState(false);
  // Read once on mount: a failed OIDC callback returns here with a reason code
  // in the query string.
  const [error, setError] = useState<string>(() => (browserMode ? readOIDCError() : ""));
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!browserMode) {
      setStatus({ ...initialStatus, authenticated: true });
      return;
    }
    browserAuth
      .status()
      .then((payload) => {
        const next = { ...initialStatus, ...payload };
        setStatus(next);
        setBrowseRoot(next.browseRoot || "");
        setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
        initializeBrowserBridge(
          next.csrfToken || "",
          !!next.nativeBrowseEnabled,
          !!next.caseInsensitivePaths,
        );
      })
      .catch((err) => {
        setError(String(err));
        setStatus(initialStatus);
        initializeBrowserBridge("", false);
      });
  }, [browserMode]);

  if (status === null) {
    return (
      <div className="web-auth-shell">
        <div className="web-auth-card">Loading web UI...</div>
      </div>
    );
  }

  if (!browserMode) {
    return <App />;
  }

  if (status.authenticated) {
    const submitBrowsePolicy = async (event?: FormEvent<HTMLFormElement>) => {
      event?.preventDefault();
      if (submitting || (!allowUnrestrictedBrowse && !browseRoot.trim())) {
        return;
      }
      setSubmitting(true);
      setError("");
      try {
        const payload = await browserAuth.saveBrowsePolicy(browseRoot, allowUnrestrictedBrowse);
        const next = { ...initialStatus, ...(payload as Partial<AuthStatus>) };
        setStatus(next);
        setBrowseRoot(next.browseRoot || "");
        setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
        updateBrowserCSRFToken(next.csrfToken || "", !!next.caseInsensitivePaths);
        initializeBrowserBridge(
          next.csrfToken || "",
          !!next.nativeBrowseEnabled,
          !!next.caseInsensitivePaths,
        );
      } catch (err) {
        setError(String(err));
      } finally {
        setSubmitting(false);
      }
    };

    if (status.needsBrowsePolicy) {
      return (
        <div className="web-auth-shell">
          <div className="web-auth-card">
            <p className="web-auth-card__eyebrow">upbrr Web</p>
            <h1>Set Browse Access</h1>
            <p className="web-auth-card__copy">
              Choose the host directories this web UI can browse, or explicitly allow unrestricted
              host browsing. Separate multiple paths with commas.
            </p>
            <form onSubmit={submitBrowsePolicy}>
              <label>
                <span>Browse root</span>
                <input
                  value={browseRoot}
                  onChange={(event) => setBrowseRoot(event.target.value)}
                  disabled={allowUnrestrictedBrowse}
                  placeholder="D:\\Media, E:\\Downloads"
                />
              </label>
              <div className="web-auth-card__checkbox">
                <Checkbox
                  id="allow-unrestricted-browse"
                  checked={allowUnrestrictedBrowse}
                  onCheckedChange={setAllowUnrestrictedBrowse}
                />
                <label htmlFor="allow-unrestricted-browse">Allow unrestricted host browsing</label>
              </div>
              {error ? <p className="web-auth-card__error">{error}</p> : null}
              <button
                type="submit"
                disabled={submitting || (!allowUnrestrictedBrowse && !browseRoot.trim())}
              >
                {submitting ? "Saving..." : "Continue"}
              </button>
            </form>
          </div>
        </div>
      );
    }

    return (
      <div className="web-shell">
        <div className="auth-bar">
          <span className="auth-username">{status.username}</span>
          <button
            type="button"
            className="auth-logout"
            onClick={async () => {
              await browserAuth.logout();
              updateBrowserCSRFToken("");
              window.location.reload();
            }}
          >
            Logout
          </button>
        </div>
        <App />
      </div>
    );
  }

  const submit = async (event?: FormEvent<HTMLFormElement>) => {
    event?.preventDefault();
    if (submitting || !username.trim() || !password.trim()) {
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      const payload = status.needsSetup
        ? await browserAuth.bootstrap(username, password, retainLogin)
        : await browserAuth.login(username, password, retainLogin);
      const next = { ...initialStatus, ...(payload as Partial<AuthStatus>) };
      setStatus(next);
      setBrowseRoot(next.browseRoot || "");
      setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
      updateBrowserCSRFToken(next.csrfToken || "", !!next.caseInsensitivePaths);
      initializeBrowserBridge(
        next.csrfToken || "",
        !!next.nativeBrowseEnabled,
        !!next.caseInsensitivePaths,
      );
    } catch (err) {
      setError(String(err));
    } finally {
      setSubmitting(false);
    }
  };

  const ssoOnly = status.oidcEnabled && status.oidcDisableBuiltInLogin;
  const heading = ssoOnly ? "Sign In" : status.needsSetup ? "Create Admin Account" : "Sign In";
  const copy = ssoOnly
    ? "Sign in with your identity provider to access the web workflow."
    : status.needsSetup
      ? "Set up the single-user web account for this instance."
      : "Authenticate to access the local web workflow.";

  return (
    <div className="web-auth-shell">
      <div className="web-auth-card">
        <p className="web-auth-card__eyebrow">upbrr Web</p>
        <h1>{heading}</h1>
        <p className="web-auth-card__copy">{copy}</p>
        {status.oidcEnabled ? (
          <>
            {/* A link, not a fetch: the provider redirect must navigate the
                top-level browsing context, and XHR cannot follow it. */}
            <a
              className="web-auth-card__sso"
              href={withBrowserBasePath("/api/auth/oidc/login")}
              rel="nofollow"
            >
              Sign in with SSO
            </a>
            {ssoOnly ? null : <p className="web-auth-card__divider">or</p>}
          </>
        ) : null}
        {ssoOnly ? (
          error ? (
            <p className="web-auth-card__error">{error}</p>
          ) : null
        ) : (
          <form onSubmit={submit}>
            <label>
              <span>Username</span>
              <input
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
              />
            </label>
            <label>
              <span>Password</span>
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete={status.needsSetup ? "new-password" : "current-password"}
              />
            </label>
            <div className="web-auth-card__checkbox">
              <Checkbox id="retain-login" checked={retainLogin} onCheckedChange={setRetainLogin} />
              <label htmlFor="retain-login">Keep me signed in on this device</label>
            </div>
            {error ? <p className="web-auth-card__error">{error}</p> : null}
            <button type="submit" disabled={submitting || !username.trim() || !password.trim()}>
              {submitting ? "Working..." : status.needsSetup ? "Create Account" : "Sign In"}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
