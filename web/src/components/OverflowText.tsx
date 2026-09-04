import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

const openDelay = 300;
const closeDelay = 100;
const viewportGap = 8;

export function OverflowText({
  emptyLabel,
  mono = false,
  testId,
  value,
}: {
  emptyLabel: string;
  mono?: boolean;
  testId?: string;
  value: string;
}) {
  const tooltipId = useId();
  const triggerRef = useRef<HTMLSpanElement>(null);
  const tooltipRef = useRef<HTMLSpanElement>(null);
  const openTimerRef = useRef<number | null>(null);
  const closeTimerRef = useRef<number | null>(null);
  const [overflowing, setOverflowing] = useState(false);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ left: viewportGap, top: viewportGap });

  const clearTimers = useCallback(() => {
    if (openTimerRef.current !== null) window.clearTimeout(openTimerRef.current);
    if (closeTimerRef.current !== null) window.clearTimeout(closeTimerRef.current);
    openTimerRef.current = null;
    closeTimerRef.current = null;
  }, []);

  const measure = useCallback(() => {
    const trigger = triggerRef.current;
    const next = value !== "" && trigger !== null && trigger.scrollWidth > trigger.clientWidth;
    setOverflowing(next);
    if (!next) {
      clearTimers();
      setOpen(false);
    }
  }, [clearTimers, value]);

  const placeTooltip = useCallback(() => {
    const trigger = triggerRef.current;
    const tooltip = tooltipRef.current;
    if (trigger === null || tooltip === null) return;
    const triggerRect = trigger.getBoundingClientRect();
    const tooltipRect = tooltip.getBoundingClientRect();
    const left = Math.min(
      Math.max(viewportGap, triggerRect.left),
      Math.max(viewportGap, window.innerWidth - tooltipRect.width - viewportGap),
    );
    const below = triggerRect.bottom + 6;
    const top = below + tooltipRect.height <= window.innerHeight - viewportGap
      ? below
      : Math.max(viewportGap, triggerRect.top - tooltipRect.height - 6);
    setPosition({ left, top });
  }, []);

  useLayoutEffect(() => {
    measure();
    const observer = typeof ResizeObserver === "function"
      ? new ResizeObserver(measure)
      : null;
    if (triggerRef.current !== null) observer?.observe(triggerRef.current);
    window.addEventListener("resize", measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [measure, value]);

  useLayoutEffect(() => {
    if (!open) return;
    placeTooltip();
    window.addEventListener("resize", placeTooltip);
    window.addEventListener("scroll", placeTooltip, true);
    return () => {
      window.removeEventListener("resize", placeTooltip);
      window.removeEventListener("scroll", placeTooltip, true);
    };
  }, [open, placeTooltip]);

  useEffect(() => clearTimers, [clearTimers]);

  function scheduleOpen() {
    if (!overflowing) return;
    clearTimers();
    openTimerRef.current = window.setTimeout(() => {
      setOpen(true);
      openTimerRef.current = null;
    }, openDelay);
  }

  function scheduleClose() {
    clearTimers();
    closeTimerRef.current = window.setTimeout(() => {
      setOpen(false);
      closeTimerRef.current = null;
    }, closeDelay);
  }

  return (
    <>
      <span
        ref={triggerRef}
        className={`overflow-text${mono ? " overflow-text-mono" : ""}${value === "" ? " overflow-text-empty" : ""}`}
        data-testid={testId}
        tabIndex={overflowing ? 0 : undefined}
        aria-describedby={open ? tooltipId : undefined}
        onMouseEnter={scheduleOpen}
        onMouseLeave={scheduleClose}
        onFocus={scheduleOpen}
        onBlur={scheduleClose}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            clearTimers();
            setOpen(false);
          }
        }}
      >
        {value === "" ? emptyLabel : value}
      </span>
      {open ? createPortal(
        <span
          ref={tooltipRef}
          id={tooltipId}
          className="overflow-tooltip"
          role="tooltip"
          style={{ left: position.left, top: position.top }}
          onMouseEnter={clearTimers}
          onMouseLeave={scheduleClose}
        >
          {value}
        </span>,
        document.body,
      ) : null}
    </>
  );
}
