import { useState } from "react";
import { useTranslation } from "react-i18next";

type CopyState = "idle" | "copying" | "copied" | "failed";

export function AgentAccessPage() {
  const { t } = useTranslation("common");
  const skillURL = new URL("/skills/confighub/SKILL.md", window.location.origin).href;
  const prompt = t("agentAccess.prompt", { url: skillURL, server: window.location.origin });

  return (
    <section className="resource-page agent-access-page" aria-labelledby="agent-access-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">ConfigHub / Agent</p>
          <h1 id="agent-access-title">{t("agentAccess.title")}</h1>
          <p>{t("agentAccess.summary")}</p>
        </div>
      </header>
      <div className="agent-access-content">
        <CopyField
          id="agent-skill-url"
          label={t("agentAccess.urlLabel")}
          value={skillURL}
          button={t("agentAccess.copyURL")}
        />
        <a href={skillURL} target="_blank" rel="noreferrer">{t("agentAccess.openSkill")}</a>
        <CopyField
          id="agent-prompt"
          label={t("agentAccess.promptLabel")}
          value={prompt}
          button={t("agentAccess.copyPrompt")}
          multiline
        />
        <section className="agent-access-notes" aria-labelledby="agent-setup-title">
          <h2 id="agent-setup-title">{t("agentAccess.setupTitle")}</h2>
          <p>{t("agentAccess.installation")}</p>
          <p>{t("agentAccess.credentials")}</p>
        </section>
      </div>
    </section>
  );
}

function CopyField({ id, label, value, button, multiline = false }: {
  id: string;
  label: string;
  value: string;
  button: string;
  multiline?: boolean;
}) {
  const { t } = useTranslation("common");
  const [state, setState] = useState<CopyState>("idle");

  async function copy() {
    setState("copying");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      setState("failed");
    }
  }

  return (
    <div className="form-field agent-copy-field">
      <label htmlFor={id}>{label}</label>
      {multiline ? (
        <textarea id={id} value={value} rows={7} readOnly onFocus={(event) => event.currentTarget.select()} />
      ) : (
        <input id={id} value={value} readOnly onFocus={(event) => event.currentTarget.select()} />
      )}
      <button className="secondary-button" type="button" disabled={state === "copying"} onClick={() => void copy()}>
        {state === "copying" ? t("agentAccess.copying") : button}
      </button>
      {state === "copied" ? <p role="status">{t("agentAccess.copied")}</p> : null}
      {state === "failed" ? <p role="alert">{t("agentAccess.copyFailed")}</p> : null}
    </div>
  );
}
