// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FormEvent, ReactNode } from "react";
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

const authFieldClass = "grid gap-[0.45rem]";
const authInputClass =
  "w-full rounded-[0.9rem] border border-foreground/10 bg-card/70 px-[0.95rem] py-[0.8rem] text-foreground";
const authActionClass =
  "min-h-11 rounded-xl bg-primary px-4 py-[0.9rem] font-bold text-primary-foreground disabled:cursor-wait disabled:opacity-65";

function AuthShell({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <div className="grid min-h-screen place-items-center bg-background p-8">
      <div className="grid w-full max-w-md gap-4 rounded-3xl border border-foreground/10 bg-card/85 p-8 shadow-xl">
        {children}
      </div>
    </div>
  );
}

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
    return <AuthShell>Loading web UI...</AuthShell>;
  }

  if (sessionChanged) {
    return (
      <AuthShell>
        <h1>Session changed</h1>
        <p className="m-0 text-muted-foreground">{sessionChangedMessage}</p>
        <button className={authActionClass} type="button" onClick={() => window.location.reload()}>
          Reload tab
        </button>
      </AuthShell>
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
          <AuthShell>
            <p className="m-0 text-[0.78rem] tracking-[0.12em] text-[var(--primary-text)] uppercase">
              upbrr Web
            </p>
            <h1>Set Browse Access</h1>
            <p className="m-0 text-muted-foreground">
              Choose the host directories this web UI can browse, or explicitly allow unrestricted
              host browsing. Later changes require the local upbrr binary. Separate multiple paths
              with commas.
            </p>
            <form className="grid gap-3" onSubmit={submitInitialBrowsePolicy}>
              <label className={authFieldClass}>
                <span>Browse root</span>
                <input
                  className={authInputClass}
                  value={browseRoot}
                  onChange={(event) => setBrowseRoot(event.target.value)}
                  disabled={allowUnrestrictedBrowse}
                  placeholder="D:\\Media, E:\\Downloads"
                />
              </label>
              <div className="flex items-center gap-3 text-foreground">
                <Checkbox
                  id="allow-unrestricted-browse"
                  checked={allowUnrestrictedBrowse}
                  onCheckedChange={setAllowUnrestrictedBrowse}
                />
                <label className="cursor-pointer" htmlFor="allow-unrestricted-browse">
                  Allow unrestricted host browsing
                </label>
              </div>
              {error ? <p className="m-0 text-destructive-text">{error}</p> : null}
              <button
                className={authActionClass}
                type="submit"
                disabled={submitting || (!allowUnrestrictedBrowse && !browseRoot.trim())}
              >
                {submitting ? "Saving..." : "Continue"}
              </button>
            </form>
          </AuthShell>
        );
      }

      return (
        <AuthShell>
          <p className="m-0 text-[0.78rem] tracking-[0.12em] text-[var(--primary-text)] uppercase">
            upbrr Web
          </p>
          <h1>Browse Access Required</h1>
          <p className="m-0 text-muted-foreground">
            Browse access can only be changed from the local upbrr binary. Stop the server, run the
            command below, then restart it.
          </p>
          <code>upbrr auth browse-roots &lt;path&gt;...</code>
        </AuthShell>
      );
    }

    return (
      <div className="min-h-screen">
        <div className="fixed top-3 right-3 z-[1100] flex items-center gap-2 max-[960px]:static max-[960px]:justify-end max-[960px]:px-3 max-[960px]:pt-2">
          <span className="hidden h-8 items-center rounded-lg border border-border bg-card px-3 text-sm text-muted-foreground md:inline-flex">
            {status.username}
          </span>
          <button
            type="button"
            className="inline-flex h-8 items-center justify-center rounded-lg border border-border bg-secondary px-3 text-sm font-semibold text-secondary-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
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
    <AuthShell>
      <p className="m-0 text-[0.78rem] tracking-[0.12em] text-[var(--primary-text)] uppercase">
        upbrr Web
      </p>
      <h1>{status.needsSetup ? "Create Admin Account" : "Sign In"}</h1>
      <p className="m-0 text-muted-foreground">
        {status.needsSetup
          ? "Set up the single-user web account for this instance."
          : "Authenticate to access the local web workflow."}
      </p>
      <form className="grid gap-3" onSubmit={submit}>
        <label className={authFieldClass}>
          <span>Username</span>
          <input
            className={authInputClass}
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            autoComplete="username"
          />
        </label>
        <label className={authFieldClass}>
          <span>Password</span>
          <input
            className={authInputClass}
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete={status.needsSetup ? "new-password" : "current-password"}
          />
        </label>
        <div className="flex items-center gap-3 text-foreground">
          <Checkbox id="retain-login" checked={retainLogin} onCheckedChange={setRetainLogin} />
          <label className="cursor-pointer" htmlFor="retain-login">
            Keep me signed in on this device
          </label>
        </div>
        {error ? <p className="m-0 text-destructive-text">{error}</p> : null}
        <button
          className={authActionClass}
          type="submit"
          disabled={submitting || !username.trim() || !password.trim()}
        >
          {submitting ? "Working..." : status.needsSetup ? "Create Account" : "Sign In"}
        </button>
      </form>
    </AuthShell>
  );
}
