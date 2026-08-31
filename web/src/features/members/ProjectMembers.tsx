import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { APIError } from "../../api/client";
import type { MemberGrant, MemberPermission } from "../../api/types";
import { useAuth } from "../../auth/AuthProvider";
import { ModalDialog } from "../../components/ModalDialog";
import { localizePresentFields } from "../../i18n/apiErrors";

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

type MemberErrorKey =
  | "project.validation.username"
  | "project.validation.permission"
  | "project.validation.checkFields"
  | "project.errors.reconciliation"
  | "project.errors.alreadyInProgress"
  | "project.errors.addNotConfirmed"
  | "project.errors.saveNotConfirmed"
  | "project.errors.removeNotConfirmed"
  | "project.errors.addNotFound"
  | "project.errors.grantNotFound"
  | "project.errors.conflict"
  | "project.errors.addFailed"
  | "project.errors.saveFailed"
  | "project.errors.removeFailed";
type MemberStatus =
  | {
      kind:
        | "loading"
        | "refreshing"
        | "loaded"
        | "unavailable"
        | "confirmationRequired"
        | "accessSaved";
    }
  | { kind: "permissionUpdated"; name: string; permission: MemberPermission }
  | { kind: "accessRemoved"; name: string }
  | null;
type MutationAction = "add" | "save" | "remove";
type MemberMutationErrorState = {
  kind: "mutation";
  action: MutationAction;
  status: number | null;
  code: string | null;
};
type MemberError = MemberErrorKey | MemberMutationErrorState;
type MemberMutationMessages = Record<
  MutationAction,
  { notFound: string; conflict: string; failure: string }
>;

const usernamePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/u;

export function ProjectMembers({
  canManage,
  projectSlug,
}: {
  canManage: boolean;
  projectSlug: string;
}) {
  const { client } = useAuth();
  const { t } = useTranslation(["members", "common"]);
  const [members, setMembers] = useState<MemberGrant[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [username, setUsername] = useState("");
  const [newPermission, setNewPermission] =
    useState<MemberPermission>("viewer");
  const [addSubmitting, setAddSubmitting] = useState(false);
  const addSubmittingRef = useRef(false);
  const [addError, setAddError] = useState<MemberError | null>(null);
  const [addFields, setAddFields] = useState<
    Partial<Record<"username" | "permission", MemberErrorKey>>
  >({});
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
  const [rowErrors, setRowErrors] = useState<
    Record<string, MemberError | null>
  >({});
  const [confirmation, setConfirmation] = useState<MemberGrant | null>(null);
  const [removeError, setRemoveError] = useState<MemberError | null>(null);
  const [status, setStatus] = useState<MemberStatus>({ kind: "loading" });
  const [reconciliationError, setReconciliationError] =
    useState<MemberErrorKey | null>(null);
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
    setRemoveError(null);
    setReconciliationError(null);
    appliedMemberListRequestRef.current = 0;
    appliedMembersRef.current = [];
    setStatus({ kind: "loading" });
    setLoading(true);
    setLoadError(false);
    void refreshMembers(projectSlug, projectGeneration).then((result) => {
      if (projectGenerationRef.current === projectGeneration) {
        if (result.kind === "failed") {
          setLoadError(true);
          setStatus({ kind: "unavailable" });
        } else if (result.kind === "applied") {
          setStatus({ kind: "loaded" });
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
      setLoadError(false);
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
    setLoadError(false);
    setStatus({ kind: reconcilingMutation ? "refreshing" : "loading" });
    const result = await refreshMembers(projectSlug, projectGeneration);
    if (projectGenerationRef.current !== projectGeneration) {
      return;
    }
    if (result.kind === "applied" || result.kind === "superseded") {
      if (!reconcilingMutation) {
        setStatus({ kind: "loaded" });
      }
    } else if (result.kind === "failed") {
      if (reconcilingMutation) {
        setReconciliationError("project.errors.reconciliation");
        setStatus({ kind: "confirmationRequired" });
      } else {
        setLoadError(true);
        setStatus({ kind: "unavailable" });
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
    setReconciliationError("project.errors.reconciliation");
    setStatus({ kind: "confirmationRequired" });
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
      setReconciliationError(null);
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
          setAddError(null);
          setStatus({ kind: "accessSaved" });
        } else {
          setAddError("project.errors.addNotConfirmed");
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
            ? null
            : "project.errors.saveNotConfirmed",
        }));
        if (confirmed) {
          setStatus({
            kind: "permissionUpdated",
            name: reconciliation.displayName,
            permission: reconciliation.permission,
          });
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
        setRemoveError(null);
        setStatus({
          kind: "accessRemoved",
          name: reconciliation.displayName,
        });
        window.requestAnimationFrame(() => statusRef.current?.focus());
      } else {
        setRemoveError("project.errors.removeNotConfirmed");
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
    setStatus(null);
    setAddError(null);
    setAddFields({});
    if (!usernamePattern.test(username)) {
      setAddFields({ username: "project.validation.username" });
      setAddError("project.validation.username");
      return;
    }

    const operation = startRowOperation(username, "adding");
    if (operation === null) {
      setAddError("project.errors.alreadyInProgress");
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
      setStatus({ kind: "accessSaved" });
    } else if (mutationError instanceof APIError && mutationError.status === 422) {
      setAddFields(
        localizePresentFields(mutationError.fields, {
          username: "project.validation.username",
          permission: "project.validation.permission",
        }) as Partial<
          Record<"username" | "permission", MemberErrorKey>
        >,
      );
      setAddError("project.validation.checkFields");
    } else if (refresh.kind === "failed") {
      setAddError(null);
      queueReconciliation({
        kind: "add",
        username: operationUsername,
        permission,
        minimumRequest: refresh.request,
      });
    } else if (mutationError !== null) {
      setAddError(toMemberMutationErrorState(mutationError, "add"));
    } else if (mutationSucceeded) {
      setAddError("project.errors.addNotConfirmed");
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
    setStatus(null);
    setRowErrors((current) => ({ ...current, [member.username]: null }));
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
      setStatus({
        kind: "permissionUpdated",
        name: member.display_name,
        permission,
      });
    } else if (refresh.kind === "failed") {
      setRowErrors((current) => ({ ...current, [member.username]: null }));
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
        [member.username]: toMemberMutationErrorState(mutationError, "save"),
      }));
    } else {
      setRowErrors((current) => ({
        ...current,
        [member.username]: "project.errors.saveNotConfirmed",
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
    setRemoveError(null);
    setStatus(null);
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
      setStatus({ kind: "accessRemoved", name: member.display_name });
      window.requestAnimationFrame(() => statusRef.current?.focus());
    } else if (refresh.kind === "failed") {
      setRemoveError(null);
      queueReconciliation({
        kind: "remove",
        username: member.username,
        displayName: member.display_name,
        minimumRequest: refresh.request,
      });
    } else if (mutationError !== null) {
      setRemoveError(
        toMemberMutationErrorState(mutationError, "remove"),
      );
    } else {
      setRemoveError("project.errors.removeNotConfirmed");
    }
    finishRowOperation(member.username, operation, projectGeneration);
  }

  const addAwaitingConfirmation = Object.values(pendingReconciliations).some(
    (reconciliation) => reconciliation.kind === "add",
  );
  const permissionLabel = (permission: MemberPermission) =>
    t(`permissions.${permission}`);
  const statusMessage = status === null
    ? ""
    : status.kind === "permissionUpdated"
      ? t("project.status.permissionUpdated", {
          name: status.name,
          permission: permissionLabel(status.permission),
        })
      : status.kind === "accessRemoved"
        ? t("project.status.accessRemoved", { name: status.name })
        : t(`project.status.${status.kind}`);

  return (
    <section className="members-panel" aria-labelledby="project-members-title">
      <header className="section-heading">
        <div>
          <p className="section-index">{t("project.index")}</p>
          <h2 id="project-members-title">{t("project.title")}</h2>
          <p>{t("project.summary")}</p>
        </div>
      </header>

      {canManage ? (
        <form
          className="member-add-form"
          noValidate
          onSubmit={(event) => void handleAdd(event)}
        >
          <div className="form-field member-username-field">
            <label htmlFor="member-username">{t("project.add.username")}</label>
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
                {t(addFields.username)}
              </p>
            ) : (
              <p className="field-help" id="member-username-help">
                {t("project.add.usernameHelp")}
              </p>
            )}
          </div>
          <div className="form-field">
            <label htmlFor="new-member-permission">{t("project.add.permission")}</label>
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
              <option value="viewer">{permissionLabel("viewer")}</option>
              <option value="editor">{permissionLabel("editor")}</option>
            </select>
            {addFields.permission ? (
              <p className="field-error" id="new-member-permission-error">
                {t(addFields.permission)}
              </p>
            ) : null}
          </div>
          <button
            className="primary-button compact-button"
            type="submit"
            disabled={addSubmitting || addAwaitingConfirmation}
          >
            {t(addSubmitting ? "project.add.pending" : "project.add.action")}
          </button>
          <div className="form-message member-form-message" aria-live="polite">
            {addError ? <p role="alert">{memberErrorMessage(addError, t)}</p> : null}
          </div>
        </form>
      ) : (
        <p className="read-only-note">{t("project.readOnly")}</p>
      )}

      <div
        ref={statusRef}
        className="sr-status"
        role="status"
        aria-label={t("project.statusLabel")}
        aria-live="polite"
        tabIndex={-1}
      >
        {statusMessage}
      </div>
      {loading ? <p className="loading-line">{t("project.loading")}</p> : null}
      {reconciliationError && confirmation === null ? (
        <div role="alert">
          <p>{t(reconciliationError)}</p>
          <button
            className="secondary-button"
            type="button"
            disabled={loading}
            onClick={() => void retryMemberLoad()}
          >
            {t(loading ? "project.retrying" : "project.retryRegister")}
          </button>
        </div>
      ) : null}
      {loadError ? (
        <div role="alert">
          <p>{t("project.errors.load")}</p>
          <button
            className="secondary-button"
            type="button"
            onClick={() => void retryMemberLoad()}
          >
            {t("common:actions.retry")}
          </button>
        </div>
      ) : null}
      {!loading && !loadError && members.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>{t("project.emptyTitle")}</h3>
          <p>
            {canManage
              ? t("project.emptyManage")
              : t("project.emptyReadOnly")}
          </p>
        </div>
      ) : null}
      {!loading && !loadError && members.length > 0 ? (
        <ul className="member-list" aria-label={t("project.currentGrants")}>
          {members.map((member) => {
            const rowOperation = rowOperations[member.username];
            const isSaving = rowOperation?.kind === "saving";
            const rowBusy =
              rowOperation !== undefined ||
              pendingReconciliations[member.username] !== undefined;
            const rowError = rowErrors[member.username];
            return (
              <li
                key={member.user_id}
                aria-label={t("project.rowLabel", {
                  name: member.display_name,
                })}
              >
                <div className="member-identity">
                  <strong>{member.display_name}</strong>
                  <span>@{member.username}</span>
                </div>
                {canManage ? (
                  <div className="member-controls">
                    <label
                      className="sr-only"
                      htmlFor={`permission-${member.user_id}`}
                    >
                      {t("project.permissionFor", { username: member.username })}
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
                      <option value="viewer">{permissionLabel("viewer")}</option>
                      <option value="editor">{permissionLabel("editor")}</option>
                    </select>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={rowBusy}
                      onClick={() => void savePermission(member)}
                    >
                      {t(isSaving ? "project.saving" : "project.savePermission")}
                    </button>
                    <button
                      className="danger-button"
                      type="button"
                      disabled={rowBusy}
                      onClick={() => {
                        setRemoveError(null);
                        setConfirmation(member);
                      }}
                    >
                      {t("project.removeAccess")}
                    </button>
                    {rowError ? (
                      <p className="row-error" role="alert">
                        {memberErrorMessage(rowError, t)}
                      </p>
                    ) : null}
                  </div>
                ) : (
                  <span className="permission-label">
                    {permissionLabel(member.permission)}
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
              setRemoveError(null);
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
  error: MemberError | null;
  member: MemberGrant;
  onCancel(): void;
  onRemove(): void;
  onRetryReconciliation(): void;
  reconciliationError: MemberError | null;
  removing: boolean;
  retryingReconciliation: boolean;
}) {
  const { t } = useTranslation(["members", "common"]);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const dialogError = error ?? reconciliationError;

  return (
    <ModalDialog
      className="confirmation-panel"
      labelledBy="remove-member-title"
      describedBy="remove-member-description"
      initialFocusRef={cancelRef}
      closeDisabled={removing}
      onRequestClose={onCancel}
    >
      <p className="section-index">{t("project.dialog.index")}</p>
      <h2 id="remove-member-title">
        {t("project.dialog.title", { name: member.display_name })}
      </h2>
      <p id="remove-member-description">
        {t("project.dialog.description", {
          name: member.display_name,
          username: member.username,
        })}
      </p>
      {dialogError ? (
        <p className="confirmation-error" role="alert">
          {memberErrorMessage(dialogError, t)}
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
          {t("common:actions.cancel")}
        </button>
        {reconciliationError ? (
          <button
            className="secondary-button"
            type="button"
            disabled={retryingReconciliation}
            onClick={onRetryReconciliation}
          >
            {t(
              retryingReconciliation
                ? "project.retrying"
                : "project.retryRegister",
            )}
          </button>
        ) : null}
        <button
          className="danger-button"
          type="button"
          disabled={removing || awaitingReconciliation}
          onClick={onRemove}
        >
          {t(removing ? "project.removing" : "project.removeAccess")}
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

function toMemberMutationErrorState(
  error: unknown,
  action: MutationAction,
): MemberMutationErrorState {
  return {
    kind: "mutation",
    action,
    status: error instanceof APIError ? error.status : null,
    code: error instanceof APIError ? error.code : null,
  };
}

function memberMutationError(
  error: Pick<MemberMutationErrorState, "status" | "code">,
  action: MutationAction,
  messages: MemberMutationMessages,
): string {
  const actionMessages = messages[action];
  if (error.status === 404 || error.code === "not_found") {
    return actionMessages.notFound;
  }
  if (error.status === 409 || error.code === "resource_conflict") {
    return actionMessages.conflict;
  }
  return actionMessages.failure;
}

function memberErrorMessage(error: MemberError, t: TFunction): string {
  if (typeof error === "string") {
    return t(error);
  }
  return memberMutationError(error, error.action, {
    add: {
      notFound: t("project.errors.addNotFound"),
      conflict: t("project.errors.conflict"),
      failure: t("project.errors.addFailed"),
    },
    save: {
      notFound: t("project.errors.grantNotFound"),
      conflict: t("project.errors.conflict"),
      failure: t("project.errors.saveFailed"),
    },
    remove: {
      notFound: t("project.errors.grantNotFound"),
      conflict: t("project.errors.conflict"),
      failure: t("project.errors.removeFailed"),
    },
  });
}
