import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { UserRegister } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { formatDateTime } from "../i18n/format";
import type { SupportedLocale } from "../i18n/locales";

type LoadState = "loading" | "ready" | "error";

export function MembersPage() {
  const { client } = useAuth();
  const { t } = useTranslation("members");
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
          <p className="eyebrow">{t("directory.eyebrow")}</p>
          <h1 id="members-title">{t("directory.title")}</h1>
          <p>{t("directory.summary")}</p>
        </div>
      </header>

      {state === "loading" ? (
        <p className="loading-line" role="status">
          {t("directory.loading")}
        </p>
      ) : null}
      {state === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <p className="section-index">{t("directory.errorIndex")}</p>
          <h2>{t("directory.errorTitle")}</h2>
          <p>{t("directory.errorDescription")}</p>
          <button className="secondary-button" type="button" onClick={() => void load()}>
            {t("directory.retry")}
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
  const { i18n, t } = useTranslation(["members", "common"]);
  const locale: SupportedLocale =
    i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";

  return (
    <section
      className="administration-register"
      aria-labelledby="member-register-title"
    >
      <header className="section-heading administration-section-heading">
        <div>
          <p className="section-index">{t("directory.registerIndex")}</p>
          <h2 id="member-register-title">{t("directory.registerTitle")}</h2>
          <p>
            {t("directory.lastSync", {
              date: formatDateTime(
                register.last_successful_user_sync_at,
                locale,
                t("directory.timeUnavailable"),
              ),
            })}
          </p>
        </div>
      </header>
      {register.users.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>{t("directory.emptyTitle")}</h3>
          <p>{t("directory.emptyDescription")}</p>
        </div>
      ) : (
        <div className="data-table-wrap administration-table-wrap">
          <table
            className="data-table administration-table"
            aria-label={t("directory.registerTitle")}
          >
            <thead>
              <tr>
                <th scope="col">{t("directory.username")}</th>
                <th scope="col">{t("directory.displayName")}</th>
                <th scope="col">{t("directory.role")}</th>
                <th scope="col">{t("directory.state")}</th>
                <th scope="col">{t("directory.synchronized")}</th>
              </tr>
            </thead>
            <tbody>
              {register.users.map((user) => (
                <tr key={user.id}>
                  <th scope="row">
                    <span className="code-label">{user.username}</span>
                  </th>
                  <td>{user.display_name}</td>
                  <td>{t(`common:roles.${user.role}`)}</td>
                  <td>
                    <span
                      className={`state-label ${user.enabled ? "state-positive" : "state-muted"}`}
                    >
                      {t(user.enabled ? "directory.enabled" : "directory.disabled")}
                    </span>
                  </td>
                  <td>
                    <time dateTime={user.updated_at}>
                      {formatDateTime(
                        user.updated_at,
                        locale,
                        t("directory.timeUnavailable"),
                      )}
                    </time>
                  </td>
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
  if (
    !isRecord(value) ||
    !Array.isArray(value.users) ||
    typeof value.last_successful_user_sync_at !== "string"
  ) {
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
