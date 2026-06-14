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
};

export default function WebRoot() {
  const browserMode = isBrowserMode();
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [retainLogin, setRetainLogin] = useState(false);
  const [browseRoot, setBrowseRoot] = useState("");
  const [allowUnrestrictedBrowse, setAllowUnrestrictedBrowse] = useState(false);
  const [error, setError] = useState("");
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

  return (
    <div className="web-auth-shell">
      <div className="web-auth-card">
        <p className="web-auth-card__eyebrow">upbrr Web</p>
        <h1>{status.needsSetup ? "Create Admin Account" : "Sign In"}</h1>
        <p className="web-auth-card__copy">
          {status.needsSetup
            ? "Set up the single-user web account for this instance."
            : "Authenticate to access the local web workflow."}
        </p>
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
      </div>
    </div>
  );
}
