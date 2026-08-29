import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { APIError } from "../../api/client";
import type { MemberGrant, MemberPermission } from "../../api/types";
import { useAuth } from "../../auth/AuthProvider";

interface MemberListResponse {
  members: MemberGrant[];
}

const usernamePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/u;

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
  const [savingUsername, setSavingUsername] = useState("");
  const savingUsernamesRef = useRef(new Set<string>());
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});
  const [confirmation, setConfirmation] = useState<MemberGrant | null>(null);
  const [removing, setRemoving] = useState(false);
  const removingRef = useRef(false);
  const [removeError, setRemoveError] = useState("");
  const [status, setStatus] = useState("");
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    let current = true;
    setLoading(true);
    setLoadError("");
    void client
      .get<MemberListResponse>(memberCollectionPath(projectSlug))
      .then((response) => {
        if (current) {
          applyMemberList(response.members, setMembers, setPermissionDrafts);
        }
      })
      .catch(() => {
        if (current) {
          setLoadError(
            "Project members couldn’t be loaded. Check the server and try again.",
          );
        }
      })
      .finally(() => {
        if (current) {
          setLoading(false);
        }
      });
    return () => {
      current = false;
    };
  }, [client, projectSlug]);

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

    addSubmittingRef.current = true;
    setAddSubmitting(true);
    try {
      await client.putNoContent(memberPath(projectSlug, username), {
        permission: newPermission,
      });
      if (!mountedRef.current) {
        return;
      }
      setUsername("");
      setNewPermission("viewer");
      try {
        const response = await client.get<MemberListResponse>(
          memberCollectionPath(projectSlug),
        );
        if (mountedRef.current) {
          applyMemberList(response.members, setMembers, setPermissionDrafts);
          setStatus("Member access saved.");
        }
      } catch {
        if (mountedRef.current) {
          setAddError(
            "Access was saved, but the member list couldn’t be refreshed. Reload this page to confirm the current grants.",
          );
        }
      }
    } catch (error) {
      if (!mountedRef.current) {
        return;
      }
      if (error instanceof APIError && error.status === 422) {
        setAddFields(error.fields);
        setAddError("Check the marked member fields and try again.");
      } else {
        setAddError(memberMutationError(error, "add"));
      }
    } finally {
      if (mountedRef.current) {
        addSubmittingRef.current = false;
        setAddSubmitting(false);
      }
    }
  }

  async function savePermission(member: MemberGrant) {
    if (savingUsernamesRef.current.has(member.username)) {
      return;
    }
    const permission = permissionDrafts[member.username] ?? member.permission;
    savingUsernamesRef.current.add(member.username);
    setSavingUsername(member.username);
    setStatus("");
    setRowErrors((current) => ({ ...current, [member.username]: "" }));
    try {
      await client.putNoContent(memberPath(projectSlug, member.username), {
        permission,
      });
      if (mountedRef.current) {
        setMembers((current) =>
          current.map((item) =>
            item.username === member.username ? { ...item, permission } : item,
          ),
        );
        setStatus(`Permission saved for ${member.display_name}.`);
      }
    } catch (error) {
      if (mountedRef.current) {
        setRowErrors((current) => ({
          ...current,
          [member.username]: memberMutationError(error, "save"),
        }));
      }
    } finally {
      savingUsernamesRef.current.delete(member.username);
      if (mountedRef.current) {
        setSavingUsername("");
      }
    }
  }

  async function removeMember() {
    if (confirmation === null || removingRef.current) {
      return;
    }
    const member = confirmation;
    removingRef.current = true;
    setRemoving(true);
    setRemoveError("");
    setStatus("");
    try {
      await client.delete(memberPath(projectSlug, member.username));
      if (mountedRef.current) {
        setMembers((current) =>
          current.filter((item) => item.username !== member.username),
        );
        setPermissionDrafts((current) => {
          const next = { ...current };
          delete next[member.username];
          return next;
        });
        setConfirmation(null);
        setStatus(`Access removed for ${member.display_name}.`);
      }
    } catch (error) {
      if (mountedRef.current) {
        setRemoveError(memberMutationError(error, "remove"));
      }
    } finally {
      removingRef.current = false;
      if (mountedRef.current) {
        setRemoving(false);
      }
    }
  }

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
              disabled={addSubmitting}
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
              disabled={addSubmitting}
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
            disabled={addSubmitting}
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

      <div className="sr-status" role="status" aria-live="polite">
        {status}
      </div>
      {loading ? <p role="status">Loading project members…</p> : null}
      {loadError ? <p role="alert">{loadError}</p> : null}
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
            const isSaving = savingUsername === member.username;
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
                      disabled={isSaving}
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
                      disabled={isSaving}
                      onClick={() => void savePermission(member)}
                    >
                      {isSaving ? "Saving…" : "Save permission"}
                    </button>
                    <button
                      className="danger-button"
                      type="button"
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
          removing={removing}
          onCancel={() => {
            if (!removing) {
              setConfirmation(null);
              setRemoveError("");
            }
          }}
          onRemove={() => void removeMember()}
        />
      ) : null}
    </section>
  );
}

function RemoveMemberDialog({
  error,
  member,
  onCancel,
  onRemove,
  removing,
}: {
  error: string;
  member: MemberGrant;
  onCancel(): void;
  onRemove(): void;
  removing: boolean;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    cancelRef.current?.focus();
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape" && !removing) {
        onCancel();
      }
    }
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [onCancel, removing]);

  return (
    <div className="dialog-backdrop">
      <section
        className="dialog-panel confirmation-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="remove-member-title"
        aria-describedby="remove-member-description"
      >
        <p className="section-index">Access register / Confirmation</p>
        <h2 id="remove-member-title">Remove {member.display_name} access</h2>
        <p id="remove-member-description">
          Remove project access for {member.display_name} (@{member.username})?
          This takes effect immediately.
        </p>
        {error ? (
          <p className="confirmation-error" role="alert">
            {error}
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
          <button
            className="danger-button"
            type="button"
            disabled={removing}
            onClick={onRemove}
          >
            {removing ? "Removing access…" : "Remove access"}
          </button>
        </div>
      </section>
    </div>
  );
}

function applyMemberList(
  members: MemberGrant[],
  setMembers: React.Dispatch<React.SetStateAction<MemberGrant[]>>,
  setPermissionDrafts: React.Dispatch<
    React.SetStateAction<Record<string, MemberPermission>>
  >,
) {
  setMembers(members);
  setPermissionDrafts(
    Object.fromEntries(
      members.map((member) => [member.username, member.permission]),
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
