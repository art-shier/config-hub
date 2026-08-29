import type { APIClientContract, APIErrorEnvelope } from "./types";

const API_PREFIX = "/api/v1";
const UNEXPECTED_RESPONSE_MESSAGE =
  "The server returned an unexpected response.";

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId: string,
    public fields: Record<string, string>,
  ) {
    super(message);
    this.name = "APIError";
  }
}

export class APIClient implements APIClientContract {
  constructor(
    private readonly getCSRFToken: () => string | null,
    private readonly onUnauthorized: () => void = () => undefined,
  ) {}

  get<T>(path: string): Promise<T> {
    return this.request<T>("GET", path);
  }

  post<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>("POST", path, body);
  }

  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>("PUT", path, body);
  }

  async delete(path: string): Promise<void> {
    await this.request<void>("DELETE", path);
  }

  private async request<T>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    body?: unknown,
  ): Promise<T> {
    const url = safeAPIURL(path);
    const headers = new Headers({ Accept: "application/json" });
    const hasBody = method === "POST" || method === "PUT";

    if (hasBody) {
      headers.set("Content-Type", "application/json");
    }
    if (method !== "GET") {
      const csrfToken = this.getCSRFToken();
      if (csrfToken) {
        headers.set("X-CSRF-Token", csrfToken);
      }
    }

    const response = await fetch(url, {
      method,
      headers,
      credentials: "same-origin",
      redirect: "error",
      body: hasBody ? JSON.stringify(body) : undefined,
    });
    const responseText = response.status === 204 ? "" : await response.text();

    if (!response.ok) {
      if (response.status === 401) {
        try {
          this.onUnauthorized();
        } catch {
          // Authentication cleanup must not replace the typed server error.
        }
      }
      throw toAPIError(response.status, responseText);
    }

    if (responseText === "") {
      return undefined as T;
    }

    try {
      return JSON.parse(responseText) as T;
    } catch {
      throw unexpectedResponse(response.status);
    }
  }
}

function safeAPIURL(path: string): string {
  if (
    !path.startsWith("/") ||
    path.startsWith("//") ||
    path.includes("#") ||
    /[\\\u0000-\u001f\u007f]/u.test(path)
  ) {
    throw new TypeError("Expected a safe relative API path.");
  }

  const origin = new URL(window.location.origin);
  const url = new URL(`${API_PREFIX}${path}`, origin);
  if (
    url.origin !== origin.origin ||
    (url.pathname !== API_PREFIX &&
      !url.pathname.startsWith(`${API_PREFIX}/`))
  ) {
    throw new TypeError("Expected a safe relative API path.");
  }
  return url.toString();
}

function toAPIError(status: number, responseText: string): APIError {
  try {
    const envelope = JSON.parse(responseText) as unknown;
    if (isAPIErrorEnvelope(envelope)) {
      return new APIError(
        status,
        envelope.error.code,
        envelope.error.message,
        envelope.error.request_id,
        envelope.error.fields,
      );
    }
  } catch {
    // Fall through to a bounded error without echoing the response body.
  }
  return unexpectedResponse(status);
}

function unexpectedResponse(status: number): APIError {
  return new APIError(
    status,
    "unexpected_response",
    UNEXPECTED_RESPONSE_MESSAGE,
    "",
    {},
  );
}

function isAPIErrorEnvelope(value: unknown): value is APIErrorEnvelope {
  if (!isRecord(value) || !isRecord(value.error)) {
    return false;
  }
  const { code, fields, message, request_id: requestId } = value.error;
  return (
    typeof code === "string" &&
    typeof message === "string" &&
    typeof requestId === "string" &&
    isStringRecord(fields)
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return (
    isRecord(value) &&
    Object.values(value).every((field) => typeof field === "string")
  );
}

export type { APIClientContract } from "./types";
