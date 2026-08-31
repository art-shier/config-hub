import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SystemStatus } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { formatDateTime } from "../i18n/format";
import type { SupportedLocale } from "../i18n/locales";

type LoadState = "loading" | "ready" | "error";

export function SystemPage() {
  const { client } = useAuth();
  const { t } = useTranslation("system");
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
          <p className="eyebrow">{t("page.eyebrow")}</p>
          <h1 id="system-title">{t("page.title")}</h1>
          <p>{t("page.summary")}</p>
        </div>
      </header>

      {state === "loading" ? <p className="loading-line" role="status">{t("page.loading")}</p> : null}
      {state === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <p className="section-index">{t("error.index")}</p>
          <h2>{t("error.title")}</h2>
          <p>{t("error.description")}</p>
          <button className="secondary-button" type="button" onClick={() => void load()}>
            {t("error.retry")}
          </button>
        </div>
      ) : null}
      {state === "ready" && status !== null ? <SystemRegister status={status} /> : null}
    </section>
  );
}

function SystemRegister({ status }: { status: SystemStatus }) {
  const { i18n, t } = useTranslation("system");
  const locale: SupportedLocale = i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
  const rows = [
    [t("register.buildVersion"), status.build_version || t("status.unavailable"), null],
    [t("register.live"), status.live ? t("status.available") : t("status.unavailable"), status.live],
    [t("register.ready"), status.ready ? t("status.available") : t("status.unavailable"), status.ready],
    [t("register.sqliteReadiness"), status.sqlite_ready ? t("status.available") : t("status.unavailable"), status.sqlite_ready],
    [t("register.lastSuccessfulUserSync"), formatDateTime(status.last_successful_user_sync_at, locale, t("status.unavailable")), null],
  ] as const;
  return (
    <section className="system-register" aria-labelledby="system-register-title">
      <header className="section-heading administration-section-heading">
        <div>
          <p className="section-index">{t("register.index")}</p>
          <h2 id="system-register-title">{t("register.title")}</h2>
          <p>{t("register.safety")}</p>
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
