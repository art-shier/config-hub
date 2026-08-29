import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { APIError } from "../../api/client";
import type { MemberGrant, MemberPermission } from "../../api/types";
import { useAuth } from "../../auth/AuthProvider";
import { ModalDialog } from "../../components/ModalDialog";

interface MemberListResponse {
  members: MemberGrant[];
}

type RowOperationKind = "adding" | "saving" | "removing";
type RowOperation = { kind: RowOperationKind; token: number };
type MemberRefreshResult =
  | { kind: "applied"; members: MemberGrant[] }
  | { kind: "failed"; request: number }
  | { kind: "superseded"; members: MemberGrant[] }
  | { kind: "stale" };
type PendingReconciliation = (
  | {
      kind: "add";
      username: string;
      permission: MemberPermission;
    }
  | {
      kind: "save";
      username: string;
      displayName: string;
      permission: MemberPermission;
    }
  | {
      kind: "remove";
      username: string;
      displayName: string;
    }
) & { minimumRequest: number };

const usernamePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/u;
const reconciliationMessage =
  "An access change may have been saved, but the member register has not confirmed it. Retry the register to load the authoritative grants.";

export function ProjectMembers({
  canManage,
  projectSlug,
}: {
  canManage: boolean;
  projectSlug: string;
}) {
  const { client } = useAuth();
  const [members, setMembers] = useState<MemberGrant[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [username, setUsername] = useState("");
  const [newPermission, setNewPermission] =
    useState<MemberPermission>("viewer");
  const [addSubmitting, setAddSubmitting] = useState(false);
  const addSubmittingRef = useRef(false);
  const [addError, setAddError] = useState("");
  const [addFields, setAddFields] = useState<Record<string, string>>({});
  const [permissionDrafts, setPermissionDrafts] = useState<
    Record<string, MemberPermission>
  >({});
  const [rowOperations, setRowOperations] = useState<
    Record<string, RowOperation>
  >({});
  const rowOperationsRef = useRef(new Map<string, RowOperation>());
  const [pendingReconciliations, setPendingReconciliations] = useState<
    Record<string, PendingReconciliation>
  >({});
  const pendingReconciliationsRef = useRef(
    new Map<string, PendingReconciliation>(),
  );
  const operationTokenRef = useRef(0);
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});
  const [confirmation, setConfirmation] = useState<MemberGrant | null>(null);
  const [removeError, setRemoveError] = useState("");
  const [status, setStatus] = useState("");
  const [reconciliationError, setReconciliationError] = useState("");
  const statusRef = useRef<HTMLDivElement>(null);
  const projectGenerationRef = useRef(0);
  const memberListRequestRef = useRef(0);
  const appliedMemberListRequestRef = useRef(0);
  const appliedMembersRef = useRef<MemberGrant[]>([]);

  useEffect(() => {
    const projectGeneration = ++projectGenerationRef.current;
    rowOperationsRef.current.clear();
    pendingReconciliationsRef.current.clear();
    setRowOperations({});
    setPendingReconciliations({});
    addSubmittingRef.current = false;
    setAddSubmitting(false);
    setMembers([]);
    setPermissionDrafts({});
    setRowErrors({});
    setConfirmation(null);
    setRemoveError("");
    setReconciliationError("");
    appliedMemberListRequestRef.current = 0;
    appliedMembersRef.current = [];
    setStatus("Loading project members…");
    setLoading(true);
    setLoadError("");
    void refreshMembers(projectSlug, projectGeneration).then((result) => {
      if (projectGenerationRef.current === projectGeneration) {
        if (result.kind === "failed") {
          setLoadError(
            "Project members couldn’t be loaded. Check the server and try again.",
          );
          setStatus("Project members unavailable.");
        } else if (result.kind === "applied") {
          setStatus("Project members loaded.");
        }
        setLoading(false);
      }
    });
    return () => {
      if (projectGenerationRef.current === projectGeneration) {
        projectGenerationRef.current += 1;
      }
      memberListRequestRef.current += 1;
    };
  }, [client, projectSlug]);

  async function refreshMembers(
    requestedProjectSlug: string,
    projectGeneration: number,
  ): Promise<MemberRefreshResult> {
    const request = ++memberListRequestRef.current;
    try {
      const response = await client.get<MemberListResponse>(
        memberCollectionPath(requestedProjectSlug),
      );
      if (projectGenerationRef.current !== projectGeneration) {
        return { kind: "stale" };
      }
      if (request < appliedMemberListRequestRef.current) {
        return { kind: "superseded", members: appliedMembersRef.current };
      }
      appliedMemberListRequestRef.current = request;
      appliedMembersRef.current = response.members;
      const preservedUsernames = new Set([
        ...rowOperationsRef.current.keys(),
        ...pendingReconciliationsRef.current.keys(),
      ]);
      applyMemberList(
        response.members,
        setMembers,
        setPermissionDrafts,
        preservedUsernames,
      );
      setLoadError("");
      resolvePendingReconciliations(
        response.members,
        request,
        projectGeneration,
      );
      return { kind: "applied", members: response.members };
    } catch {
      if (projectGenerationRef.current !== projectGeneration) {
        return { kind: "stale" };
      }
      return request < appliedMemberListRequestRef.current
        ? { kind: "superseded", members: appliedMembersRef.current }
        : { kind: "failed", request };
    }
  }

  async function retryMemberLoad() {
    const projectGeneration = projectGenerationRef.current;
    const reconcilingMutation = pendingReconciliationsRef.current.size > 0;
    setLoading(true);
    setLoadError("");
    setStatus(
      reconcilingMutation
        ? "Refreshing the member register…"
        : "Loading project members…",
    );
    const result = await refreshMembers(projectSlug, projectGeneration);
    if (projectGenerationRef.current !== projectGeneration) {
      return;
    }
    if (result.kind === "applied" || result.kind === "superseded") {
      if (!reconcilingMutation) {
        setStatus("Project members loaded.");
      }
    } else if (result.kind === "failed") {
      if (reconcilingMutation) {
        setReconciliationError(reconciliationMessage);
        setStatus("Member register confirmation required.");
      } else {
        setLoadError(
          "Project members couldn’t be loaded. Check the server and try again.",
        );
        setStatus("Project members unavailable.");
      }
    }
    setLoading(false);
  }

  function queueReconciliation(reconciliation: PendingReconciliation) {
    pendingReconciliationsRef.current.set(
      reconciliation.username,
      reconciliation,
    );
    setPendingReconciliations((current) => ({
      ...current,
      [reconciliation.username]: reconciliation,
    }));
    setReconciliationError(reconciliationMessage);
    setStatus("Member register confirmation required.");
  }

  function resolvePendingReconciliations(
    authoritativeMembers: MemberGrant[],
    appliedRequest: number,
    projectGeneration: number,
  ) {
    if (
      projectGenerationRef.current !== projectGeneration ||
      pendingReconciliationsRef.current.size === 0
    ) {
      return;
    }
    const pending = [...pendingReconciliationsRef.current.values()].filter(
      (reconciliation) => reconciliation.minimumRequest <= appliedRequest,
    );
    if (pending.length === 0) {
      return;
    }
    for (const reconciliation of pending) {
      pendingReconciliationsRef.current.delete(reconciliation.username);
    }
    setPendingReconciliations(
      Object.fromEntries(pendingReconciliationsRef.current.entries()),
    );
    if (pendingReconciliationsRef.current.size === 0) {
      setReconciliationError("");
    }

    for (const reconciliation of pending) {
      if (reconciliation.kind === "add") {
        const confirmed = authoritativeMembers.some(
          (member) =>
            member.username === reconciliation.username &&
            member.permission === reconciliation.permission,
        );
        if (confirmed) {
          setUsername("");
          setNewPermission("viewer");
          setAddError("");
          setStatus("Member access saved.");
        } else {
          setAddError(
            "The refreshed register did not confirm this member grant. Review it and try again if needed.",
          );
        }
        continue;
      }

      if (reconciliation.kind === "save") {
        const confirmed = authoritativeMembers.some(
          (member) =>
            member.username === reconciliation.username &&
            member.permission === reconciliation.permission,
        );
        setRowErrors((current) => ({
          ...current,
          [reconciliation.username]: confirmed
            ? ""
            : "The refreshed register did not confirm this permission change. Review it and try again if needed.",
        }));
        if (confirmed) {
          setStatus(
            `Permission for ${reconciliation.displayName} updated to ${titleCase(reconciliation.permission)}.`,
          );
        }
        continue;
      }

      const confirmedRemoved = !authoritativeMembers.some(
        (member) => member.username === reconciliation.username,
      );
      if (confirmedRemoved) {
        setConfirmation((current) =>
          current?.username === reconciliation.username ? null : current,
        );
        setRemoveError("");
        setStatus(`Access removed for ${reconciliation.displayName}.`);
        window.requestAnimationFrame(() => statusRef.current?.focus());
      } else {
        setRemoveError(
          "The refreshed register still includes this grant. Review it before trying again.",
        );
      }
    }
  }

  function startRowOperation(
    operationUsername: string,
    kind: RowOperationKind,
  ): RowOperation | null {
    if (
      rowOperationsRef.current.has(operationUsername) ||
      pendingReconciliationsRef.current.has(operationUsername)
    ) {
      return null;
    }
    const operation = { kind, token: ++operationTokenRef.current };
    rowOperationsRef.current.set(operationUsername, operation);
    setRowOperations((current) => ({
      ...current,
      [operationUsername]: operation,
    }));
    return operation;
  }

  function isCurrentRowOperation(
    operationUsername: string,
    operation: RowOperation,
    projectGeneration: number,
  ): boolean {
    return (
      projectGenerationRef.current === projectGeneration &&
      rowOperationsRef.current.get(operationUsername)?.token === operation.token
    );
  }

  function finishRowOperation(
    operationUsername: string,
    operation: RowOperation,
    projectGeneration: number,
  ) {
    if (!isCurrentRowOperation(operationUsername, operation, projectGeneration)) {
      return;
    }
    rowOperationsRef.current.delete(operationUsername);
    setRowOperations((current) => {
      const next = { ...current };
      delete next[operationUsername];
      return next;
    });
  }

  async function handleAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (addSubmittingRef.current) {
      return;
    }
    setStatus("");
    setAddError("");
    setAddFields({});
    if (!usernamePattern.test(username)) {
      const message =
        "Enter a valid synchronized username using letters, numbers, dots, underscores, or hyphens.";
      setAddFields({ username: message });
      setAddError(message);
      return;
    }

    const operation = startRowOperation(username, "adding");
    if (operation === null) {
      setAddError("An access change for this username is already in progress.");
      return;
    }
    const operationUsername = username;
    const permission = newPermission;
    const requestedProjectSlug = projectSlug;
    const projectGeneration = projectGenerationRef.current;
    addSubmittingRef.current = true;
    setAddSubmitting(true);
    let mutationError: unknown = null;
    let mutationSucceeded = false;
    try {
      await client.putNoContent(memberPath(requestedProjectSlug, operationUsername), {
        permission,
      });
      mutationSucceeded = true;
    } catch (error) {
      mutationError = error;
    }

    if (
      !isCurrentRowOperation(
        operationUsername,
        operation,
        projectGeneration,
      )
    ) {
      return;
    }
    const refresh = await refreshMembers(requestedProjectSlug, projectGeneration);
    if (
      !isCurrentRowOperation(
        operationUsername,
        operation,
        projectGeneration,
      )
    ) {
      return;
    }

    const confirmed =
      (refresh.kind === "applied" || refresh.kind === "superseded") &&
      refresh.members.some(
        (member) =>
          member.username === operationUsername && member.permission === permission,
      );
    if (confirmed) {
      setUsername("");
      setNewPermission("viewer");
      setStatus("Member access saved.");
    } else if (mutationError instanceof APIError && mutationError.status === 422) {
      setAddFields(mutationError.fields);
      setAddError("Check the marked member fields and try again.");
    } else if (refresh.kind === "failed") {
      setAddError("");
      queueReconciliation({
        kind: "add",
        username: operationUsername,
        permission,
        minimumRequest: refresh.request,
      });
    } else if (mutationError !== null) {
      setAddError(memberMutationError(mutationError, "add"));
    } else if (mutationSucceeded) {
      setAddError(
        "The server did not confirm this member grant. Review the refreshed register and try again if needed.",
      );
    }
    finishRowOperation(operationUsername, operation, projectGeneration);
    if (projectGenerationRef.current === projectGeneration) {
      addSubmittingRef.current = false;
      setAddSubmitting(false);
    }
  }

  async function savePermission(member: MemberGrant) {
    const operation = startRowOperation(member.username, "saving");
    if (operation === null) {
      return;
    }
    const permission = permissionDrafts[member.username] ?? member.permission;
    const requestedProjectSlug = projectSlug;
    const projectGeneration = projectGenerationRef.current;
    setStatus("");
    setRowErrors((current) => ({ ...current, [member.username]: "" }));
    let mutationError: unknown = null;
    try {
      await client.putNoContent(memberPath(requestedProjectSlug, member.username), {
        permission,
      });
    } catch (error) {
      mutationError = error;
    }

    if (!isCurrentRowOperation(member.username, operation, projectGeneration)) {
      return;
    }
    const refresh = await refreshMembers(requestedProjectSlug, projectGeneration);
    if (!isCurrentRowOperation(member.username, operation, projectGeneration)) {
      return;
    }

    const confirmed =
      (refresh.kind === "applied" || refresh.kind === "superseded") &&
      refresh.members.some(
        (item) =>
          item.username === member.username && item.permission === permission,
      );
    if (confirmed) {
      setStatus(
        `Permission for ${member.display_name} updated to ${titleCase(permission)}.`,
      );
    } else if (refresh.kind === "failed") {
      setRowErrors((current) => ({ ...current, [member.username]: "" }));
      queueReconciliation({
        kind: "save",
        username: member.username,
        displayName: member.display_name,
        permission,
        minimumRequest: refresh.request,
      });
    } else if (mutationError !== null) {
      setPermissionDrafts((current) => ({
        ...current,
        [member.username]: permission,
      }));
      setRowErrors((current) => ({
        ...current,
        [member.username]: memberMutationError(mutationError, "save"),
      }));
    } else {
      setRowErrors((current) => ({
        ...current,
        [member.username]:
          "The refreshed register did not confirm this permission change. Review it and try again if needed.",
      }));
    }
    finishRowOperation(member.username, operation, projectGeneration);
  }

  async function removeMember() {
    if (confirmation === null) {
      return;
    }
    const member = confirmation;
    const operation = startRowOperation(member.username, "removing");
    if (operation === null) {
      return;
    }
    const requestedProjectSlug = projectSlug;
    const projectGeneration = projectGenerationRef.current;
    setRemoveError("");
    setStatus("");
    let mutationError: unknown = null;
    try {
      await client.delete(memberPath(requestedProjectSlug, member.username));
    } catch (error) {
      mutationError = error;
    }

    if (!isCurrentRowOperation(member.username, operation, projectGeneration)) {
      return;
    }
    const refresh = await refreshMembers(requestedProjectSlug, projectGeneration);
    if (!isCurrentRowOperation(member.username, operation, projectGeneration)) {
      return;
    }

    const confirmedRemoved =
      (refresh.kind === "applied" || refresh.kind === "superseded") &&
      !refresh.members.some((item) => item.username === member.username);
    if (confirmedRemoved) {
      setConfirmation(null);
      setStatus(`Access removed for ${member.display_name}.`);
      window.requestAnimationFrame(() => statusRef.current?.focus());
    } else if (refresh.kind === "failed") {
      setRemoveError("");
      queueReconciliation({
        kind: "remove",
        username: member.username,
        displayName: member.display_name,
        minimumRequest: refresh.request,
      });
    } else if (mutationError !== null) {
      setRemoveError(memberMutationError(mutationError, "remove"));
    } else {
      setRemoveError(
        "The refreshed register still includes this grant. Review it before trying again.",
      );
    }
    finishRowOperation(member.username, operation, projectGeneration);
  }

  const addAwaitingConfirmation = Object.values(pendingReconciliations).some(
    (reconciliation) => reconciliation.kind === "add",
  );

  return (
    <section className="members-panel" aria-labelledby="project-members-title">
      <header className="section-heading">
        <div>
          <p className="section-index">Access register</p>
          <h2 id="project-members-title">Project members</h2>
          <p>Current project grants from the synchronized account directory.</p>
        </div>
      </header>

      {canManage ? (
        <form className="member-add-form" onSubmit={(event) => void handleAdd(event)}>
          <div className="form-field member-username-field">
            <label htmlFor="member-username">Synchronized username</label>
            <input
              id="member-username"
              name="username"
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              value={username}
              disabled={addSubmitting || addAwaitingConfirmation}
              aria-invalid={addFields.username ? "true" : undefined}
              aria-describedby={
                addFields.username
                  ? "member-username-error"
                  : "member-username-help"
              }
              onChange={(event) => setUsername(event.currentTarget.value)}
            />
            {addFields.username ? (
              <p className="field-error" id="member-username-error">
                {addFields.username}
              </p>
            ) : (
              <p className="field-help" id="member-username-help">
                Enter the exact username from the synchronized directory.
              </p>
            )}
          </div>
          <div className="form-field">
            <label htmlFor="new-member-permission">New member permission</label>
            <select
              id="new-member-permission"
              name="permission"
              value={newPermission}
              disabled={addSubmitting || addAwaitingConfirmation}
              aria-invalid={addFields.permission ? "true" : undefined}
              aria-describedby={
                addFields.permission ? "new-member-permission-error" : undefined
              }
              onChange={(event) =>
                setNewPermission(event.currentTarget.value as MemberPermission)
              }
            >
              <option value="viewer">Viewer</option>
              <option value="editor">Editor</option>
            </select>
            {addFields.permission ? (
              <p className="field-error" id="new-member-permission-error">
                {addFields.permission}
              </p>
            ) : null}
          </div>
          <button
            className="primary-button compact-button"
            type="submit"
            disabled={addSubmitting || addAwaitingConfirmation}
          >
            {addSubmitting ? "Adding member…" : "Add member"}
          </button>
          <div className="form-message member-form-message" aria-live="polite">
            {addError ? <p role="alert">{addError}</p> : null}
          </div>
        </form>
      ) : (
        <p className="read-only-note">
          This register is read-only. Project administrators can change access.
        </p>
      )}

      <div
        ref={statusRef}
        className="sr-status"
        role="status"
        aria-label="Member register status"
        aria-live="polite"
        tabIndex={-1}
      >
        {status}
      </div>
      {loading ? <p className="loading-line">Loading project members…</p> : null}
      {reconciliationError && confirmation === null ? (
        <div role="alert">
          <p>{reconciliationError}</p>
          <button
            className="secondary-button"
            type="button"
            disabled={loading}
            onClick={() => void retryMemberLoad()}
          >
            {loading ? "Retrying register…" : "Retry register"}
          </button>
        </div>
      ) : null}
      {loadError ? (
        <div role="alert">
          <p>{loadError}</p>
          <button
            className="secondary-button"
            type="button"
            onClick={() => void retryMemberLoad()}
          >
            Retry
          </button>
        </div>
      ) : null}
      {!loading && !loadError && members.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>No member grants</h3>
          <p>
            {canManage
              ? "Add a synchronized username to grant project access."
              : "No synchronized accounts currently have a project grant."}
          </p>
        </div>
      ) : null}
      {!loading && !loadError && members.length > 0 ? (
        <ul className="member-list" aria-label="Current project grants">
          {members.map((member) => {
            const rowOperation = rowOperations[member.username];
            const isSaving = rowOperation?.kind === "saving";
            const rowBusy =
              rowOperation !== undefined ||
              pendingReconciliations[member.username] !== undefined;
            return (
              <li key={member.user_id} aria-label={`${member.display_name} access`}>
                <div className="member-identity">
                  <strong>{member.display_name}</strong>
                  <span>@{member.username}</span>
                </div>
                {canManage ? (
                  <div className="member-controls">
                    <label className="sr-only" htmlFor={`permission-${member.user_id}`}>
                      Permission for {member.username}
                    </label>
                    <select
                      id={`permission-${member.user_id}`}
                      value={permissionDrafts[member.username] ?? member.permission}
                      disabled={rowBusy}
                      onChange={(event) => {
                        const permission = event.currentTarget
                          .value as MemberPermission;
                        setPermissionDrafts((current) => ({
                          ...current,
                          [member.username]: permission,
                        }));
                      }}
                    >
                      <option value="viewer">Viewer</option>
                      <option value="editor">Editor</option>
                    </select>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={rowBusy}
                      onClick={() => void savePermission(member)}
                    >
                      {isSaving ? "Saving…" : "Save permission"}
                    </button>
                    <button
                      className="danger-button"
                      type="button"
                      disabled={rowBusy}
                      onClick={() => {
                        setRemoveError("");
                        setConfirmation(member);
                      }}
                    >
                      Remove access
                    </button>
                    {rowErrors[member.username] ? (
                      <p className="row-error" role="alert">
                        {rowErrors[member.username]}
                      </p>
                    ) : null}
                  </div>
                ) : (
                  <span className="permission-label">
                    {titleCase(member.permission)}
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      ) : null}

      {confirmation ? (
        <RemoveMemberDialog
          member={confirmation}
          error={removeError}
          awaitingReconciliation={
            pendingReconciliations[confirmation.username]?.kind === "remove"
          }
          reconciliationError={reconciliationError}
          retryingReconciliation={loading}
          removing={
            rowOperations[confirmation.username]?.kind === "removing"
          }
          onCancel={() => {
            if (rowOperations[confirmation.username] === undefined) {
              setConfirmation(null);
              setRemoveError("");
            }
          }}
          onRemove={() => void removeMember()}
          onRetryReconciliation={() => void retryMemberLoad()}
        />
      ) : null}
    </section>
  );
}

function RemoveMemberDialog({
  awaitingReconciliation,
  error,
  member,
  onCancel,
  onRemove,
  onRetryReconciliation,
  reconciliationError,
  removing,
  retryingReconciliation,
}: {
  awaitingReconciliation: boolean;
  error: string;
  member: MemberGrant;
  onCancel(): void;
  onRemove(): void;
  onRetryReconciliation(): void;
  reconciliationError: string;
  removing: boolean;
  retryingReconciliation: boolean;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);

  return (
    <ModalDialog
      className="confirmation-panel"
      labelledBy="remove-member-title"
      describedBy="remove-member-description"
      initialFocusRef={cancelRef}
      closeDisabled={removing}
      onRequestClose={onCancel}
    >
      <p className="section-index">Access register / Confirmation</p>
      <h2 id="remove-member-title">Remove {member.display_name} access</h2>
      <p id="remove-member-description">
        Remove project access for {member.display_name} (@{member.username})? This
        takes effect immediately.
      </p>
      {error || reconciliationError ? (
        <p className="confirmation-error" role="alert">
          {error || reconciliationError}
        </p>
      ) : null}
      <div className="dialog-actions">
        <button
          ref={cancelRef}
          className="secondary-button"
          type="button"
          disabled={removing}
          onClick={onCancel}
        >
          Cancel
        </button>
        {reconciliationError ? (
          <button
            className="secondary-button"
            type="button"
            disabled={retryingReconciliation}
            onClick={onRetryReconciliation}
          >
            {retryingReconciliation ? "Retrying register…" : "Retry register"}
          </button>
        ) : null}
        <button
          className="danger-button"
          type="button"
          disabled={removing || awaitingReconciliation}
          onClick={onRemove}
        >
          {removing ? "Removing access…" : "Remove access"}
        </button>
      </div>
    </ModalDialog>
  );
}

function applyMemberList(
  members: MemberGrant[],
  setMembers: React.Dispatch<React.SetStateAction<MemberGrant[]>>,
  setPermissionDrafts: React.Dispatch<
    React.SetStateAction<Record<string, MemberPermission>>
  >,
  preserveDrafts = new Set<string>(),
) {
  setMembers(members);
  setPermissionDrafts((current) =>
    Object.fromEntries(
      members.map((member) => [
        member.username,
        preserveDrafts.has(member.username)
          ? (current[member.username] ?? member.permission)
          : member.permission,
      ]),
    ),
  );
}

function memberCollectionPath(projectSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/members`;
}

function memberPath(projectSlug: string, username: string): string {
  return `${memberCollectionPath(projectSlug)}/${encodeURIComponent(username)}`;
}

function memberMutationError(
  error: unknown,
  action: "add" | "save" | "remove",
): string {
  if (error instanceof APIError) {
    if (error.status === 404 || error.code === "not_found") {
      return action === "add"
        ? "That synchronized user or project could not be found. Check the username and try again."
        : "The member grant could not be found. Reload the register and try again.";
    }
    if (error.status === 409 || error.code === "resource_conflict") {
      return "The grant changed on the server. Reload the register before trying again.";
    }
  }
  if (action === "remove") {
    return "Access wasn’t removed. The current grant is unchanged; try again.";
  }
  if (action === "save") {
    return "Permission wasn’t saved. The current grant is unchanged; try again.";
  }
  return "Member access couldn’t be added. Keep these values and try again.";
}

function titleCase(permission: MemberPermission): string {
  return permission === "editor" ? "Editor" : "Viewer";
}
