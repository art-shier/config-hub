import { useCallback, useEffect, useRef, useState } from "react";
import type { SystemStatus } from "../api/types";
import { useAuth } from "../auth/AuthProvider";

type LoadState = "loading" | "ready" | "error";

export function SystemPage() {
  const { client } = useAuth();
  const [state, setState] = useState<LoadState>("loading");
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const generationRef = useRef(0);

  const load = useCallback(async () => {
    const generation = ++generationRef.current;
    setState("loading");
    try {
      const response = await client.get<SystemStatus>("/system");
      if (generationRef.current === generation) {
        if (!isSystemStatus(response)) {
          throw new Error("invalid system status");
        }
        setStatus(response);
        setState("ready");
      }
    } catch {
      if (generationRef.current === generation) {
        setStatus(null);
        setState("error");
      }
    }
  }, [client]);

  useEffect(() => {
    void load();
    return () => {
      generationRef.current += 1;
    };
  }, [load]);

  return (
    <section className="resource-page administration-page" aria-labelledby="system-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">Service state register</p>
          <h1 id="system-title">System</h1>
          <p>A deliberately narrow view of process, storage, and account synchronization readiness.</p>
        </div>
      </header>

      {state === "loading" ? <p className="loading-line" role="status">Loading system state…</p> : null}
      {state === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <p className="section-index">Operational register / Unavailable</p>
          <h2>System state unavailable</h2>
          <p>The safe service summary couldn’t be loaded. Check the service and try again.</p>
          <button className="secondary-button" type="button" onClick={() => void load()}>
            Retry system state
          </button>
        </div>
      ) : null}
      {state === "ready" && status !== null ? <SystemRegister status={status} /> : null}
    </section>
  );
}

function SystemRegister({ status }: { status: SystemStatus }) {
  const rows = [
    ["Build version", status.build_version || "Unavailable", null],
    ["Live", status.live ? "Available" : "Unavailable", status.live],
    ["Ready", status.ready ? "Available" : "Unavailable", status.ready],
    ["SQLite readiness", status.sqlite_ready ? "Available" : "Unavailable", status.sqlite_ready],
    ["Last successful user sync", formatDateTime(status.last_successful_user_sync_at), null],
  ] as const;
  return (
    <section className="system-register" aria-labelledby="system-register-title">
      <header className="section-heading administration-section-heading">
        <div>
          <p className="section-index">Current process / Safe fields</p>
          <h2 id="system-register-title">Operational state</h2>
          <p>Paths, configuration values, database details, and user-file contents are never shown.</p>
        </div>
      </header>
      <dl className="status-ledger">
        {rows.map(([label, value, healthy]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd className={healthy === null ? undefined : healthy ? "state-positive" : "state-negative"}>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function isSystemStatus(value: unknown): value is SystemStatus {
  return isRecord(value) &&
    typeof value.build_version === "string" &&
    typeof value.live === "boolean" &&
    typeof value.ready === "boolean" &&
    typeof value.sqlite_ready === "boolean" &&
    typeof value.last_successful_user_sync_at === "string";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function formatDateTime(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf())
    ? "Unavailable"
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed);
}
