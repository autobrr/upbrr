// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useState } from "react";
import App from "./app";
import { browserAuth, initializeBrowserBridge, isBrowserMode, updateBrowserCSRFToken } from "./utils/runtime";

type AuthStatus = {
  authenticated: boolean;
  needsSetup: boolean;
  username: string;
  csrfToken: string;
  nativeBrowseEnabled: boolean;
};

const initialStatus: AuthStatus = {
  authenticated: false,
  needsSetup: false,
  username: "",
  csrfToken: "",
  nativeBrowseEnabled: false
};

export default function WebRoot() {
  const browserMode = isBrowserMode();
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [retainLogin, setRetainLogin] = useState(false);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!browserMode) {
      setStatus({ ...initialStatus, authenticated: true });
      return;
    }
    browserAuth.status().then((payload) => {
      const next = { ...initialStatus, ...payload };
      setStatus(next);
      initializeBrowserBridge(next.csrfToken || "", !!next.nativeBrowseEnabled);
    }).catch((err) => {
      setError(String(err));
      setStatus(initialStatus);
      initializeBrowserBridge("", false);
    });
  }, [browserMode]);

  if (status === null) {
    return <div className="web-auth-shell"><div className="web-auth-card">Loading web UI...</div></div>;
  }

  if (!browserMode) {
    return <App />;
  }

  if (status.authenticated) {
    return (
      <div className="web-shell">
        <div className="web-shell__bar">
          <span>Signed in as {status.username}</span>
          <button
            type="button"
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

  const submit = async () => {
    setSubmitting(true);
    setError("");
    try {
      const payload = status.needsSetup
        ? await browserAuth.bootstrap(username, password, retainLogin)
        : await browserAuth.login(username, password, retainLogin);
      const next = { ...initialStatus, ...(payload as Partial<AuthStatus>) };
      setStatus(next);
      updateBrowserCSRFToken(next.csrfToken || "");
      initializeBrowserBridge(next.csrfToken || "", !!next.nativeBrowseEnabled);
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
        <label>
          <span>Username</span>
          <input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" />
        </label>
        <label>
          <span>Password</span>
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete={status.needsSetup ? "new-password" : "current-password"} />
        </label>
        <label className="web-auth-card__checkbox">
          <input type="checkbox" checked={retainLogin} onChange={(event) => setRetainLogin(event.target.checked)} />
          <span>Keep me signed in on this device</span>
        </label>
        {error ? <p className="web-auth-card__error">{error}</p> : null}
        <button type="button" onClick={submit} disabled={submitting || !username.trim() || !password.trim()}>
          {submitting ? "Working..." : status.needsSetup ? "Create Account" : "Sign In"}
        </button>
      </div>
    </div>
  );
}
