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

export interface ProjectDetail extends Project {
  permission: ProjectPermission;
  environments: Environment[];
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
