export type UserRole = "admin" | "member";

export interface User {
  id: string;
  username: string;
  display_name: string;
  role: UserRole;
}

export interface Session {
  user: User;
  csrf_token: string;
  expires_at: string;
}

export interface LoginCredentials {
  username: string;
  password: string;
}

export interface Project {
  id: string;
  slug: string;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export type ProjectPermission = "admin" | "viewer" | "editor";

export interface Environment {
  id: string;
  project_id: string;
  slug: string;
  name: string;
  current_revision_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface MachineEnvironmentGrant {
  project_id: string;
  environment_id: string;
}

export interface MachineTokenMetadata {
  id: string;
  name: string;
  prefix: string;
  expires_at: string;
  revoked_at: string | null;
  created_at: string;
}

export interface IssuedMachineToken {
  id: string;
  name: string;
  prefix: string;
  plaintext: string;
  expires_at: string;
  created_at: string;
}

export interface MachineIdentity {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MachineIdentityDetail extends MachineIdentity {
  grants: MachineEnvironmentGrant[];
  tokens: MachineTokenMetadata[];
}

export interface SynchronizedUser {
  id: string;
  username: string;
  display_name: string;
  role: UserRole;
  enabled: boolean;
  updated_at: string;
}

export interface UserRegister {
  users: SynchronizedUser[];
  last_successful_user_sync_at: string;
}

export interface SystemStatus {
  build_version: string;
  live: boolean;
  ready: boolean;
  sqlite_ready: boolean;
  last_successful_user_sync_at: string;
}

export interface ProjectDetail extends Project {
  permission: ProjectPermission;
  environments: Environment[];
}

export interface ConfigEntry {
  key: string;
  value: string;
  service: string;
}

export interface RevisionSummary {
  id: string;
  environment_id: string;
  message: string;
  created_by: string;
  version: number;
  created_at: string;
}

export interface Revision extends RevisionSummary {
  entries: ConfigEntry[];
}

export type ChangeKind = "added" | "changed" | "deleted";

export interface RevisionChange {
  key: string;
  kind: ChangeKind;
  before: string;
  after: string;
  before_service: string;
  after_service: string;
}

export interface DiffResult {
  before_revision: number;
  after_revision: number;
  changes: RevisionChange[];
}

export type MemberPermission = "viewer" | "editor";

export interface MemberGrant {
  user_id: string;
  username: string;
  display_name: string;
  permission: MemberPermission;
}

export interface APIErrorBody {
  code: string;
  message: string;
  request_id: string;
  fields: Record<string, string>;
}

export interface APIErrorEnvelope {
  error: APIErrorBody;
}

export interface APIClientContract {
  get<T>(path: string): Promise<T>;
  post<T>(path: string, body: unknown): Promise<T>;
  postNoContent(path: string, body?: unknown): Promise<void>;
  put<T>(path: string, body: unknown): Promise<T>;
  putNoContent(path: string, body: unknown): Promise<void>;
  delete(path: string): Promise<void>;
}
