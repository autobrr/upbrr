// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FormEvent } from "react";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Checkbox } from "./components/ui/checkbox";

const App = lazy(() => import("./app"));
import {
  authClient,
  initializeWebClient,
  sessionChangedMessage,
  subscribeWebSessionLoss,
  updateWebCSRFToken,
} from "./api/client";

type AuthStatus = {
  authenticated: boolean;
  needsSetup: boolean;
  username: string;
  csrfToken: string;
  caseInsensitivePaths: boolean;
  browseRoot: string;
  allowUnrestrictedBrowse: boolean;
  needsBrowsePolicy: boolean;
  canInitializeBrowsePolicy: boolean;
};

const initialStatus: AuthStatus = {
  authenticated: false,
  needsSetup: false,
  username: "",
  csrfToken: "",
  caseInsensitivePaths: false,
  browseRoot: "",
  allowUnrestrictedBrowse: false,
  needsBrowsePolicy: false,
  canInitializeBrowsePolicy: false,
};

/** Gates the embedded app on Web authentication and initial browse setup. */
export default function WebRoot() {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [retainLogin, setRetainLogin] = useState(false);
  const [browseRoot, setBrowseRoot] = useState("");
  const [allowUnrestrictedBrowse, setAllowUnrestrictedBrowse] = useState(false);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [sessionChanged, setSessionChanged] = useState(false);
  const logoutApproved = useRef(false);

  useEffect(() => {
    authClient
      .status()
      .then((payload) => {
        const next = { ...initialStatus, ...payload };
        setStatus(next);
        setBrowseRoot(next.browseRoot || "");
        setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
        initializeWebClient(next.csrfToken || "", !!next.caseInsensitivePaths);
      })
      .catch((err) => {
        setError(String(err));
        setStatus(initialStatus);
        initializeWebClient("");
      });
  }, []);

  useEffect(
    () =>
      subscribeWebSessionLoss((reason) => {
        updateWebCSRFToken("");
        setPassword("");
        setSettingsDirty(false);
        setStatus(initialStatus);
        setSessionChanged(reason === "changed");
        setError(
          reason === "lost"
            ? "Your session ended. Sign in again; unsaved settings were discarded."
            : "",
        );
      }),
    [],
  );

  useEffect(() => {
    if (!status?.authenticated || !settingsDirty) return;
    const warnOnDeparture = (event: BeforeUnloadEvent) => {
      if (logoutApproved.current) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warnOnDeparture);
    return () => window.removeEventListener("beforeunload", warnOnDeparture);
  }, [settingsDirty, status?.authenticated]);

  if (status === null) {
    return (
      <div className="web-auth-shell">
        <div className="web-auth-card">Loading web UI...</div>
      </div>
    );
  }

  if (sessionChanged) {
    return (
      <div className="web-auth-shell">
        <div className="web-auth-card">
          <h1>Session changed</h1>
          <p className="web-auth-card__copy">{sessionChangedMessage}</p>
          <button type="button" onClick={() => window.location.reload()}>
            Reload tab
          </button>
        </div>
      </div>
    );
  }

  if (status.authenticated) {
    const submitInitialBrowsePolicy = async (event?: FormEvent<HTMLFormElement>) => {
      event?.preventDefault();
      if (submitting || (!allowUnrestrictedBrowse && !browseRoot.trim())) {
        return;
      }
      setSubmitting(true);
      setError("");
      try {
        const payload = await authClient.saveInitialBrowsePolicy(
          allowUnrestrictedBrowse ? "" : browseRoot,
          allowUnrestrictedBrowse,
        );
        const next = { ...initialStatus, ...(payload as Partial<AuthStatus>) };
        setStatus(next);
        setBrowseRoot(next.browseRoot || "");
        setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
        updateWebCSRFToken(next.csrfToken || "", !!next.caseInsensitivePaths);
      } catch (err) {
        setError(String(err));
      } finally {
        setSubmitting(false);
      }
    };

    if (status.needsBrowsePolicy) {
      if (status.canInitializeBrowsePolicy) {
        return (
          <div className="web-auth-shell">
            <div className="web-auth-card">
              <p className="web-auth-card__eyebrow">upbrr Web</p>
              <h1>Set Browse Access</h1>
              <p className="web-auth-card__copy">
                Choose the host directories this web UI can browse, or explicitly allow unrestricted
                host browsing. Later changes require the local upbrr binary. Separate multiple paths
                with commas.
              </p>
              <form onSubmit={submitInitialBrowsePolicy}>
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
                  <label htmlFor="allow-unrestricted-browse">
                    Allow unrestricted host browsing
                  </label>
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
              if (settingsDirty && !window.confirm("Discard unsaved settings and sign out?")) {
                return;
              }
              logoutApproved.current = true;
              try {
                await authClient.logout();
                updateWebCSRFToken("");
                window.location.reload();
              } catch (err) {
                logoutApproved.current = false;
                setError(String(err));
              }
            }}
          >
            Logout
          </button>
        </div>
        <Suspense fallback={<p role="status">Loading workspace…</p>}>
          <App onSettingsDirtyChange={setSettingsDirty} />
        </Suspense>
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
      setPassword("");
      setBrowseRoot(next.browseRoot || "");
      setAllowUnrestrictedBrowse(!!next.allowUnrestrictedBrowse);
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
