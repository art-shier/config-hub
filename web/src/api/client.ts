import type { APIClientContract, APIErrorEnvelope } from "./types";

const API_PREFIX = "/api/v1";
const UNEXPECTED_RESPONSE_MESSAGE =
  "The server returned an unexpected response.";
type HTTPMethod = "GET" | "POST" | "PUT" | "DELETE";
type ResponseContract = "content" | "no-content";

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
    private readonly onUnauthorized: (requestGeneration: number) => void =
      () => undefined,
    private readonly getRequestGeneration: () => number = () => 0,
  ) {}

  get<T>(path: string): Promise<T> {
    return this.request<T>("GET", path, undefined, "content");
  }

  post<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>("POST", path, body, "content");
  }

  postNoContent(path: string, body?: unknown): Promise<void> {
    return this.request("POST", path, body, "no-content");
  }

  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>("PUT", path, body, "content");
  }

  putNoContent(path: string, body: unknown): Promise<void> {
    return this.request("PUT", path, body, "no-content");
  }

  delete(path: string): Promise<void> {
    return this.request("DELETE", path, undefined, "no-content");
  }

  private request<T>(
    method: HTTPMethod,
    path: string,
    body: unknown,
    responseContract: "content",
  ): Promise<T>;
  private request(
    method: HTTPMethod,
    path: string,
    body: unknown,
    responseContract: "no-content",
  ): Promise<void>;
  private async request<T>(
    method: HTTPMethod,
    path: string,
    body: unknown,
    responseContract: ResponseContract,
  ): Promise<T | void> {
    const requestGeneration = this.getRequestGeneration();
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
    const hasNoContent = response.status === 204 || response.status === 205;
    const responseText = hasNoContent ? "" : await response.text();

    if (!response.ok) {
      if (response.status === 401) {
        try {
          this.onUnauthorized(requestGeneration);
        } catch {
          // Authentication cleanup must not replace the typed server error.
        }
      }
      throw toAPIError(response.status, responseText);
    }

    if (responseContract === "no-content") {
      if (!hasNoContent) {
        throw unexpectedResponse(response.status);
      }
      return;
    }
    if (hasNoContent || responseText === "") {
      throw unexpectedResponse(response.status);
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
    /%(?:2f|5c)/iu.test(path) ||
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
