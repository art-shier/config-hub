import { useTranslation } from "react-i18next";

export function ExactValue({
  label,
  testId,
  value,
}: {
  label: string;
  testId?: string;
  value: string;
}) {
  const { t } = useTranslation("common");
  return (
    <div className="exact-value">
      {value === "" ? (
        <span className="exact-value-empty">{t("exactValue.emptyString")}</span>
      ) : null}
      <pre
        className="exact-value-control"
        aria-label={label}
        aria-readonly="true"
        data-testid={testId}
        role="textbox"
        tabIndex={0}
      >{value}</pre>
    </div>
  );
}
