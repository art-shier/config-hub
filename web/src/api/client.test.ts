import { http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";
import { server } from "../test/setup";
import { APIClient, APIError } from "./client";

describe("APIClient", () => {
  it("sends CSRF and exposes a typed conflict", async () => {
    let requestDetails:
      | {
          accept: string | null;
          contentType: string | null;
          credentials: RequestCredentials;
          csrf: string | null;
          body: unknown;
        }
      | undefined;

    server.use(
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        async ({ request }) => {
          requestDetails = {
            accept: request.headers.get("Accept"),
            contentType: request.headers.get("Content-Type"),
            credentials: request.credentials,
            csrf: request.headers.get("X-CSRF-Token"),
            body: await request.json(),
          };
          return HttpResponse.json(
            {
              error: {
                code: "revision_conflict",
                message: "changed",
                request_id: "req_1",
                fields: {},
              },
            },
            { status: 409 },
          );
        },
      ),
    );

    const client = new APIClient(() => "csrf-token");
    const request = client.put(
      "/projects/shop/environments/prod/config",
      { expected_revision: 7 },
    );

    await expect(request).rejects.toMatchObject({
      status: 409,
      code: "revision_conflict",
      message: "changed",
      requestId: "req_1",
      fields: {},
    });
    await expect(request).rejects.toBeInstanceOf(APIError);
    expect(requestDetails).toEqual({
      accept: "application/json",
      contentType: "application/json",
      credentials: "same-origin",
      csrf: "csrf-token",
      body: { expected_revision: 7 },
    });
  });

  it("does not send a null CSRF token on login", async () => {
    let csrfHeader: string | null = "not-called";

    server.use(
      http.post("/api/v1/auth/login", ({ request }) => {
        csrfHeader = request.headers.get("X-CSRF-Token");
        return HttpResponse.json({ ok: true });
      }),
    );

    const client = new APIClient(() => null);
    await client.post("/auth/login", {
      username: "admin",
      password: "password",
    });

    expect(csrfHeader).toBeNull();
  });

  it("handles JSON and no-content success responses", async () => {
    server.use(
      http.get("/api/v1/projects", () =>
        HttpResponse.json({ projects: [{ slug: "shop" }] }),
      ),
      http.delete("/api/v1/projects/shop", () =>
        new HttpResponse(null, { status: 204 }),
      ),
    );

    const client = new APIClient(() => "csrf-token");

    await expect(
      client.get<{ projects: Array<{ slug: string }> }>("/projects"),
    ).resolves.toEqual({ projects: [{ slug: "shop" }] });
    await expect(client.delete("/projects/shop")).resolves.toBeUndefined();
  });

  it.each([
    "https://attacker.example/steal",
    "//attacker.example/steal",
    "/../auth/session",
    "/%2e%2e/auth/session",
    "/projects\\..\\auth/session",
    "/projects#outside",
  ])("rejects unsafe API path %s before fetching", async (path) => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const client = new APIClient(() => null);

    await expect(client.get(path)).rejects.toThrow("safe relative API path");
    expect(fetchSpy).not.toHaveBeenCalled();

    fetchSpy.mockRestore();
  });

  it("turns malformed error responses into safe typed errors", async () => {
    server.use(
      http.get(
        "/api/v1/projects",
        () =>
          new HttpResponse("<html>upstream secret</html>", {
            status: 502,
            headers: { "Content-Type": "text/html" },
          }),
      ),
    );

    const client = new APIClient(() => null);
    const request = client.get("/projects");

    await expect(request).rejects.toMatchObject({
      status: 502,
      code: "unexpected_response",
      requestId: "",
      fields: {},
    });
    await expect(request).rejects.not.toHaveProperty(
      "message",
      expect.stringContaining("upstream secret"),
    );
    await expect(request).rejects.toBeInstanceOf(APIError);
  });

  it("reports malformed JSON success without leaking response data", async () => {
    server.use(
      http.get(
        "/api/v1/projects",
        () =>
          new HttpResponse("not-json SECRET", {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );

    const client = new APIClient(() => null);

    await expect(client.get("/projects")).rejects.toMatchObject({
      status: 200,
      code: "unexpected_response",
      message: "The server returned an unexpected response.",
    });
  });

  it("notifies the authentication boundary after any 401", async () => {
    server.use(
      http.get("/api/v1/projects", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_2",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
    );
    const onUnauthorized = vi.fn();
    const client = new APIClient(
      () => "csrf-token",
      onUnauthorized,
      () => 7,
    );

    await expect(client.get("/projects")).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).toHaveBeenCalledOnce();
    expect(onUnauthorized).toHaveBeenCalledWith(7);
  });
});
