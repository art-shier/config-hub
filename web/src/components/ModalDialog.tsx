import {
  useEffect,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";

const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export function ModalDialog({
  children,
  className = "",
  closeDisabled = false,
  describedBy,
  initialFocusRef,
  labelledBy,
  onRequestClose,
}: {
  children: ReactNode;
  className?: string;
  closeDisabled?: boolean;
  describedBy?: string;
  initialFocusRef?: RefObject<HTMLElement | null>;
  labelledBy: string;
  onRequestClose(): void;
}) {
  const dialogRef = useRef<HTMLElement>(null);
  const closeDisabledRef = useRef(closeDisabled);
  const onRequestCloseRef = useRef(onRequestClose);
  closeDisabledRef.current = closeDisabled;
  onRequestCloseRef.current = onRequestClose;

  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const dialog = dialogRef.current;
    (initialFocusRef?.current ?? focusableElements(dialog)[0] ?? dialog)?.focus();

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        if (!closeDisabledRef.current) {
          event.preventDefault();
          onRequestCloseRef.current();
        }
        return;
      }
      if (event.key !== "Tab" || dialog === null) {
        return;
      }

      const focusable = focusableElements(dialog);
      if (focusable.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && (active === first || !dialog.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      if (opener?.isConnected) {
        opener.focus();
      }
    };
  }, [initialFocusRef]);

  return (
    <div className="dialog-backdrop">
      <section
        ref={dialogRef}
        className={`dialog-panel${className ? ` ${className}` : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        aria-describedby={describedBy}
        tabIndex={-1}
      >
        {children}
      </section>
    </div>
  );
}

function focusableElements(container: HTMLElement | null): HTMLElement[] {
  return container === null
    ? []
    : Array.from(container.querySelectorAll<HTMLElement>(focusableSelector));
}
