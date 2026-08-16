// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FormEvent } from "react";
import { useEffect, useState } from "react";
import App from "./app";
import { Checkbox } from "./components/ui/checkbox";
import { authClient, initializeWebClient, updateWebCSRFToken } from "./api/client";

type AuthStatus = {
  authenticated: boolean;
  needsSetup: boolean;
  username: string;
  csrfToken: string;
  caseInsensitivePaths: boolean;
  needsBrowsePolicy: boolean;
};

const initialStatus: AuthStatus = {
  authenticated: false,
  needsSetup: false,
  username: "",
  csrfToken: "",
  caseInsensitivePaths: false,
  needsBrowsePolicy: false,
};

/** Gates the embedded app on Web authentication and CLI-managed browse policy. */
export default function WebRoot() {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [retainLogin, setRetainLogin] = useState(false);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    authClient
      .status()
      .then((payload) => {
        const next = { ...initialStatus, ...payload };
        setStatus(next);
        initializeWebClient(next.csrfToken || "", !!next.caseInsensitivePaths);
      })
      .catch((err) => {
        setError(String(err));
        setStatus(initialStatus);
        initializeWebClient("");
      });
  }, []);

  if (status === null) {
    return (
      <div className="web-auth-shell">
        <div className="web-auth-card">Loading web UI...</div>
      </div>
    );
  }

  if (status.authenticated) {
    if (status.needsBrowsePolicy) {
      return (
        <div className="web-auth-shell">
          <div className="web-auth-card">
            <p className="web-auth-card__eyebrow">upbrr Web</p>
            <h1>Browse Access Required</h1>
            <p className="web-auth-card__copy">
              Browse access can only be changed from the local upbrr binary. Stop the server, run
              the command below, then restart it.
            </p>
            <code>upbrr auth browse-roots &lt;path&gt;...</code>
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
              await authClient.logout();
              updateWebCSRFToken("");
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
        ? await authClient.bootstrap(username, password, retainLogin)
        : await authClient.login(username, password, retainLogin);
      const next = { ...initialStatus, ...(payload as Partial<AuthStatus>) };
      setStatus(next);
      updateWebCSRFToken(next.csrfToken || "", !!next.caseInsensitivePaths);
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
