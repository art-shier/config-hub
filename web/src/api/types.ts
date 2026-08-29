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
  put<T>(path: string, body: unknown): Promise<T>;
  delete(path: string): Promise<void>;
}
