import { useCallback, useEffect, useRef, useState } from "react";
import type { UserRegister } from "../api/types";
import { useAuth } from "../auth/AuthProvider";

type LoadState = "loading" | "ready" | "error";

export function MembersPage() {
  const { client } = useAuth();
  const [state, setState] = useState<LoadState>("loading");
  const [register, setRegister] = useState<UserRegister | null>(null);
  const generationRef = useRef(0);

  const load = useCallback(async () => {
    const generation = ++generationRef.current;
    setState("loading");
    try {
      const response = await client.get<UserRegister>("/users");
      if (generationRef.current === generation) {
        if (!isUserRegister(response)) {
          throw new Error("invalid user register");
        }
        setRegister(response);
        setState("ready");
      }
    } catch {
      if (generationRef.current === generation) {
        setRegister(null);
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
    <section className="resource-page administration-page" aria-labelledby="members-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">Synchronized account register</p>
          <h1 id="members-title">Members</h1>
          <p>Accounts are read from the configured user file. Credentials remain outside this interface.</p>
        </div>
      </header>

      {state === "loading" ? <p className="loading-line" role="status">Loading member register…</p> : null}
      {state === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <p className="section-index">Account synchronization / Unavailable</p>
          <h2>Member register unavailable</h2>
          <p>The synchronized account status couldn’t be loaded. Check the service and try again.</p>
          <button className="secondary-button" type="button" onClick={() => void load()}>
            Retry member register
          </button>
        </div>
      ) : null}
      {state === "ready" && register !== null ? (
        <MemberRegister register={register} />
      ) : null}
    </section>
  );
}

function MemberRegister({ register }: { register: UserRegister }) {
  return (
    <section className="administration-register" aria-labelledby="member-register-title">
      <header className="section-heading administration-section-heading">
        <div>
          <p className="section-index">Source state / Read only</p>
          <h2 id="member-register-title">Synchronized accounts</h2>
          <p>Last successful user sync: {formatDateTime(register.last_successful_user_sync_at)}</p>
        </div>
      </header>
      {register.users.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>No synchronized accounts</h3>
          <p>The last successful sync returned no account rows.</p>
        </div>
      ) : (
        <div className="data-table-wrap administration-table-wrap">
          <table className="data-table administration-table" aria-label="Synchronized accounts">
            <thead>
              <tr>
                <th scope="col">Username</th>
                <th scope="col">Display name</th>
                <th scope="col">Role</th>
                <th scope="col">State</th>
                <th scope="col">Synchronized</th>
              </tr>
            </thead>
            <tbody>
              {register.users.map((user) => (
                <tr key={user.id}>
                  <th scope="row"><span className="code-label">{user.username}</span></th>
                  <td>{user.display_name}</td>
                  <td>{titleCase(user.role)}</td>
                  <td><span className={`state-label ${user.enabled ? "state-positive" : "state-muted"}`}>{user.enabled ? "Enabled" : "Disabled"}</span></td>
                  <td><time dateTime={user.updated_at}>{formatDateTime(user.updated_at)}</time></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function isUserRegister(value: unknown): value is UserRegister {
  if (!isRecord(value) || !Array.isArray(value.users) || typeof value.last_successful_user_sync_at !== "string") {
    return false;
  }
  return value.users.every((user) =>
    isRecord(user) &&
    typeof user.id === "string" &&
    typeof user.username === "string" &&
    typeof user.display_name === "string" &&
    (user.role === "admin" || user.role === "member") &&
    typeof user.enabled === "boolean" &&
    typeof user.updated_at === "string",
  );
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

function titleCase(value: string): string {
  return value.length === 0 ? value : value[0].toLocaleUpperCase() + value.slice(1);
}
