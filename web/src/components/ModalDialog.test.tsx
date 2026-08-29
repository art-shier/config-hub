import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { describe, expect, it } from "vitest";
import { ModalDialog } from "./ModalDialog";

function ModalHarness({ closeDisabled = false }: { closeDisabled?: boolean }) {
  const [open, setOpen] = useState(false);
  const initialFocusRef = useRef<HTMLInputElement>(null);

  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open dialog
      </button>
      {open ? (
        <ModalDialog
          labelledBy="test-dialog-title"
          initialFocusRef={initialFocusRef}
          closeDisabled={closeDisabled}
          onRequestClose={() => setOpen(false)}
        >
          <h2 id="test-dialog-title">Test dialog</h2>
          <input ref={initialFocusRef} aria-label="First field" />
          <button type="button" onClick={() => setOpen(false)}>
            Cancel
          </button>
        </ModalDialog>
      ) : null}
    </>
  );
}

describe("ModalDialog", () => {
  it("moves focus inside, traps Tab in both directions, and restores the opener", async () => {
    render(<ModalHarness />);
    const user = userEvent.setup();
    const opener = screen.getByRole("button", { name: "Open dialog" });

    await user.click(opener);
    const firstField = screen.getByLabelText("First field");
    const cancel = screen.getByRole("button", { name: "Cancel" });
    expect(firstField).toHaveFocus();

    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(cancel).toHaveFocus();
    await user.tab();
    expect(firstField).toHaveFocus();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("ignores Escape while closing is disabled", async () => {
    render(<ModalHarness closeDisabled />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Open dialog" }));
    await user.keyboard("{Escape}");

    expect(screen.getByRole("dialog", { name: "Test dialog" })).toBeInTheDocument();
  });
});
