export function ExactValue({
  label,
  testId,
  value,
}: {
  label: string;
  testId?: string;
  value: string;
}) {
  return (
    <div className="exact-value">
      {value === "" ? <span className="exact-value-empty">Empty string</span> : null}
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
