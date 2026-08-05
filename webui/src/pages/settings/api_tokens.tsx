// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useState } from "react";
import * as AlertDialog from "@radix-ui/react-alert-dialog";
import {
  apiTokenClient,
  type APITokenRecord,
  type APITokenScope,
  type CreatedAPIToken,
} from "../../api/app";
import { Button } from "../../components/ui/button";

const supportedScopes: ReadonlyArray<Readonly<{ value: APITokenScope; label: string }>> = [
  { value: "workflow:read", label: "Read workflows" },
  { value: "workflow:write", label: "Prepare and update workflows" },
  { value: "workflow:execute", label: "Execute upload plans" },
];

const inputClass =
  "h-8 w-full rounded-md border border-white/10 bg-slate-950/45 px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-[rgba(53,194,193,0.18)]";

/** Manages persistent public API tokens without exposing stored hashes or revoked secrets. */
export default function APITokensSettings() {
  const [records, setRecords] = useState<APITokenRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [name, setName] = useState("Automation");
  const [ownerId, setOwnerId] = useState("default");
  const [scopes, setScopes] = useState<APITokenScope[]>(supportedScopes.map(({ value }) => value));
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedAPIToken | null>(null);
  const [copied, setCopied] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<APITokenRecord | null>(null);
  const [revoking, setRevoking] = useState(false);

  const loadRecords = async () => {
    setLoading(true);
    setError("");
    try {
      setRecords(await apiTokenClient.list());
    } catch (loadError) {
      setError(String(loadError));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void apiTokenClient
      .list()
      .then((nextRecords) => {
        if (active) setRecords(nextRecords);
      })
      .catch((loadError) => {
        if (active) setError(String(loadError));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  const toggleScope = (scope: APITokenScope) => {
    setScopes((current) =>
      current.includes(scope) ? current.filter((value) => value !== scope) : [...current, scope],
    );
  };

  const createToken = async () => {
    setCreating(true);
    setError("");
    setCreated(null);
    setCopied(false);
    try {
      const result = await apiTokenClient.create(name, ownerId, scopes);
      setCreated(result);
      setRecords((current) => [result.record, ...current]);
    } catch (createError) {
      setError(String(createError));
    } finally {
      setCreating(false);
    }
  };

  const copyToken = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.token);
      setCopied(true);
    } catch (copyError) {
      setError(`Copy failed: ${String(copyError)}`);
    }
  };

  const revokeToken = async () => {
    if (!revokeTarget) return;
    setRevoking(true);
    setError("");
    try {
      await apiTokenClient.revoke(revokeTarget.id);
      setRevokeTarget(null);
      await loadRecords();
    } catch (revokeError) {
      setError(String(revokeError));
    } finally {
      setRevoking(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <section className="settings-subgroup">
        <div>
          <h2 className="m-0 text-base font-semibold text-[var(--text)]">Generate API token</h2>
          <p className="helper">Token hashes persist in web-auth.json; plaintext is shown once.</p>
        </div>
        <div className="settings-grid">
          <label className="settings-field">
            <span>Name</span>
            <input
              className={inputClass}
              value={name}
              maxLength={100}
              onChange={(event) => setName(event.target.value)}
              placeholder="Automation"
            />
          </label>
          <label className="settings-field">
            <span>Owner</span>
            <input
              className={inputClass}
              value={ownerId}
              maxLength={100}
              onChange={(event) => setOwnerId(event.target.value)}
              placeholder="default"
            />
            <span className="helper">Tokens with the same owner share owner-scoped workflows.</span>
          </label>
        </div>
        <fieldset className="m-0 grid gap-2 border-0 p-0">
          <legend className="mb-1 text-sm font-medium text-[var(--muted)]">Scopes</legend>
          {supportedScopes.map((scope) => (
            <label
              key={scope.value}
              className="flex min-h-9 items-center gap-2 rounded-md border border-white/10 bg-white/[0.035] px-3 text-sm text-[var(--text)]"
            >
              <input
                type="checkbox"
                checked={scopes.includes(scope.value)}
                onChange={() => toggleScope(scope.value)}
              />
              <span>{scope.label}</span>
              <code className="ml-auto text-xs text-[var(--muted)]">{scope.value}</code>
            </label>
          ))}
        </fieldset>
        <div>
          <Button
            type="button"
            variant="primary"
            disabled={creating || !name.trim() || scopes.length === 0}
            onClick={() => void createToken()}
          >
            {creating ? "Generating..." : "Generate token"}
          </Button>
        </div>
        {created ? (
          <div className="grid gap-2 rounded-lg border border-amber-400/35 bg-amber-400/10 p-3">
            <p className="m-0 text-sm font-semibold text-[var(--text)]">
              Copy this token now. It cannot be shown again.
            </p>
            <div className="flex min-w-0 flex-wrap gap-2">
              <input
                aria-label="Generated API token"
                className={`${inputClass} min-w-0 flex-1 font-mono`}
                value={created.token}
                readOnly
                onFocus={(event) => event.currentTarget.select()}
              />
              <Button type="button" onClick={() => void copyToken()}>
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
          </div>
        ) : null}
        {error ? <p className="error m-0">{error}</p> : null}
      </section>

      <section className="settings-subgroup">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="m-0 text-base font-semibold text-[var(--text)]">API tokens</h2>
            <p className="helper">Revocation takes effect on the next API request.</p>
          </div>
          <Button type="button" disabled={loading} onClick={() => void loadRecords()}>
            {loading ? "Loading..." : "Reload"}
          </Button>
        </div>
        {!loading && records.length === 0 ? (
          <p className="muted">No API tokens configured.</p>
        ) : null}
        <div className="grid gap-2">
          {records.map((record) => {
            const revoked = Boolean(record.revokedAt);
            return (
              <article
                key={record.id}
                className="grid gap-2 rounded-lg border border-white/10 bg-slate-950/25 p-3"
              >
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div className="min-w-0">
                    <p className="m-0 font-semibold text-[var(--text)]">{record.name}</p>
                    <p className="helper m-0 font-mono">{record.id}</p>
                  </div>
                  <span className={`settings-auth-badge ${revoked ? "is-idle" : "is-ready"}`}>
                    {revoked ? "Revoked" : "Active"}
                  </span>
                </div>
                <p className="helper m-0">Owner: {record.ownerId}</p>
                <p className="helper m-0">Scopes: {record.scopes.join(", ")}</p>
                <p className="helper m-0">Created: {formatAPITokenDate(record.createdAt)}</p>
                {record.revokedAt ? (
                  <p className="helper m-0">Revoked: {formatAPITokenDate(record.revokedAt)}</p>
                ) : (
                  <div>
                    <Button
                      type="button"
                      className="border-red-400/30 text-red-300 hover:bg-red-400/10"
                      onClick={() => setRevokeTarget(record)}
                    >
                      Revoke
                    </Button>
                  </div>
                )}
              </article>
            );
          })}
        </div>
      </section>

      <AlertDialog.Root
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open && !revoking) setRevokeTarget(null);
        }}
      >
        <AlertDialog.Portal>
          <AlertDialog.Overlay className="import-confirm-overlay" />
          <AlertDialog.Content className="import-confirm-dialog">
            <AlertDialog.Title className="import-confirm-dialog__title">
              Revoke {revokeTarget?.name}?
            </AlertDialog.Title>
            <AlertDialog.Description className="import-confirm-dialog__message">
              Requests using this token will be rejected immediately. This cannot be undone.
            </AlertDialog.Description>
            <div className="import-confirm-dialog__actions">
              <AlertDialog.Cancel asChild>
                <Button type="button" disabled={revoking}>
                  Cancel
                </Button>
              </AlertDialog.Cancel>
              <AlertDialog.Action asChild>
                <Button
                  type="button"
                  className="border-red-400/30 text-red-300 hover:bg-red-400/10"
                  disabled={revoking}
                  onClick={(event) => {
                    event.preventDefault();
                    void revokeToken();
                  }}
                >
                  {revoking ? "Revoking..." : "Revoke token"}
                </Button>
              </AlertDialog.Action>
            </div>
          </AlertDialog.Content>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </div>
  );
}

function formatAPITokenDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}
