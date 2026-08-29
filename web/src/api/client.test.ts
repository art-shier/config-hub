import { http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";
import { server } from "../test/setup";
import { APIClient, APIError } from "./client";
import type { APIClientContract } from "./types";

describe("APIClient", () => {
  it("publishes every no-content mutation through its typed contract", () => {
    const contract: APIClientContract = new APIClient(() => "csrf-token");

    expect(contract.postNoContent).toBeTypeOf("function");
    expect(contract.putNoContent).toBeTypeOf("function");
    expect(contract.delete).toBeTypeOf("function");
  });

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

  it("handles JSON success responses", async () => {
    server.use(
      http.get("/api/v1/projects", () =>
        HttpResponse.json({ projects: [{ slug: "shop" }] }),
      ),
    );

    const client = new APIClient(() => null);

    await expect(
      client.get<{ projects: Array<{ slug: string }> }>("/projects"),
    ).resolves.toEqual({ projects: [{ slug: "shop" }] });
  });

  it("sends JSON and CSRF for a no-content PUT", async () => {
    let requestDetails:
      | {
          body: unknown;
          contentType: string | null;
          csrf: string | null;
          credentials: RequestCredentials;
        }
      | undefined;
    server.use(
      http.put(
        "/api/v1/projects/shop/members/alex.smith",
        async ({ request }) => {
          requestDetails = {
            body: await request.json(),
            contentType: request.headers.get("Content-Type"),
            csrf: request.headers.get("X-CSRF-Token"),
            credentials: request.credentials,
          };
          return new HttpResponse(null, { status: 204 });
        },
      ),
    );

    const client = new APIClient(() => "csrf-token");

    await expect(
      client.putNoContent("/projects/shop/members/alex.smith", {
        permission: "editor",
      }),
    ).resolves.toBeUndefined();
    expect(requestDetails).toEqual({
      body: { permission: "editor" },
      contentType: "application/json",
      csrf: "csrf-token",
      credentials: "same-origin",
    });
  });

  it.each([204, 205])(
    "accepts explicit %i for a no-content PUT",
    async (status) => {
      server.use(
        http.put(
          "/api/v1/projects/shop/members/alex.smith",
          () => new HttpResponse(null, { status }),
        ),
      );
      const client = new APIClient(() => "csrf-token");

      await expect(
        client.putNoContent("/projects/shop/members/alex.smith", {
          permission: "viewer",
        }),
      ).resolves.toBeUndefined();
    },
  );

  it.each([
    [200, null],
    [201, null],
    [200, { member: true }],
  ] as const)(
    "rejects status %i with body %j for a no-content PUT",
    async (status, responseBody) => {
      server.use(
        http.put("/api/v1/projects/shop/members/alex.smith", () =>
          responseBody === null
            ? new HttpResponse(null, { status })
            : HttpResponse.json(responseBody, { status }),
        ),
      );
      const client = new APIClient(() => "csrf-token");

      await expect(
        client.putNoContent("/projects/shop/members/alex.smith", {
          permission: "viewer",
        }),
      ).rejects.toMatchObject({
        status,
        code: "unexpected_response",
      });
    },
  );

  it.each([204, 205])(
    "accepts explicit %i responses for no-content methods",
    async (status) => {
      server.use(
        http.delete(
          "/api/v1/projects/shop",
          () => new HttpResponse(null, { status }),
        ),
        http.post(
          "/api/v1/auth/logout",
          () => new HttpResponse(null, { status }),
        ),
      );

      const client = new APIClient(() => "csrf-token");

      await expect(client.delete("/projects/shop")).resolves.toBeUndefined();
      await expect(
        client.postNoContent("/auth/logout", {}),
      ).resolves.toBeUndefined();
    },
  );

  it("rejects empty content-bearing success responses", async () => {
    server.use(
      http.get(
        "/api/v1/projects",
        () => new HttpResponse(null, { status: 200 }),
      ),
      http.post(
        "/api/v1/auth/login",
        () => new HttpResponse(null, { status: 201 }),
      ),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        () => new HttpResponse(null, { status: 201 }),
      ),
    );

    const client = new APIClient(() => "csrf-token");

    for (const makeRequest of [
      () => client.get<{ projects: unknown[] }>("/projects"),
      () => client.post<{ ok: true }>("/auth/login", {}),
      () =>
        client.put<{ revision: number }>(
          "/projects/shop/environments/prod/config",
          {},
        ),
    ]) {
      const request = makeRequest();
      await expect(request).rejects.toMatchObject({
        code: "unexpected_response",
        message: "The server returned an unexpected response.",
      });
      await expect(request).rejects.toBeInstanceOf(APIError);
    }
  });

  it.each([
    ["GET", 204],
    ["GET", 205],
    ["POST", 204],
    ["POST", 205],
    ["PUT", 204],
    ["PUT", 205],
  ] as const)(
    "rejects %s %i no-content responses for required-content methods",
    async (method, status) => {
      let request: Promise<unknown>;
      const client = new APIClient(() => "csrf-token");

      if (method === "GET") {
        server.use(
          http.get(
            "/api/v1/projects",
            () => new HttpResponse(null, { status }),
          ),
        );
        request = client.get<{ projects: unknown[] }>("/projects");
      } else if (method === "POST") {
        server.use(
          http.post(
            "/api/v1/auth/login",
            () => new HttpResponse(null, { status }),
          ),
        );
        request = client.post<{ ok: true }>("/auth/login", {});
      } else {
        server.use(
          http.put(
            "/api/v1/projects/shop/environments/prod/config",
            () => new HttpResponse(null, { status }),
          ),
        );
        request = client.put<{ revision: number }>(
          "/projects/shop/environments/prod/config",
          {},
        );
      }

      await expect(request).rejects.toMatchObject({
        status,
        code: "unexpected_response",
        message: "The server returned an unexpected response.",
      });
      await expect(request).rejects.toBeInstanceOf(APIError);
    },
  );

  it("rejects representations for no-content operations", async () => {
    server.use(
      http.post("/api/v1/auth/logout", () => HttpResponse.json({ ok: true })),
      http.delete("/api/v1/projects/shop", () =>
        HttpResponse.json({ deleted: true }),
      ),
    );

    const client = new APIClient(() => "csrf-token");

    await expect(client.postNoContent("/auth/logout", {})).rejects.toMatchObject(
      { status: 200, code: "unexpected_response" },
    );
    await expect(client.delete("/projects/shop")).rejects.toMatchObject({
      status: 200,
      code: "unexpected_response",
    });
  });

  it("rejects empty content-bearing success for no-content operations", async () => {
    server.use(
      http.post(
        "/api/v1/auth/logout",
        () => new HttpResponse(null, { status: 200 }),
      ),
      http.delete(
        "/api/v1/projects/shop",
        () => new HttpResponse(null, { status: 200 }),
      ),
    );

    const client = new APIClient(() => "csrf-token");

    await expect(client.postNoContent("/auth/logout", {})).rejects.toMatchObject(
      { status: 200, code: "unexpected_response" },
    );
    await expect(client.delete("/projects/shop")).rejects.toMatchObject({
      status: 200,
      code: "unexpected_response",
    });
  });

  it("preserves typed non-2xx errors for no-content operations", async () => {
    server.use(
      http.post("/api/v1/auth/logout", () =>
        HttpResponse.json(
          {
            error: {
              code: "forbidden",
              message: "logout forbidden",
              request_id: "req_logout_forbidden",
              fields: {},
            },
          },
          { status: 403 },
        ),
      ),
    );

    const client = new APIClient(() => "csrf-token");

    await expect(client.postNoContent("/auth/logout", {})).rejects.toMatchObject(
      {
        status: 403,
        code: "forbidden",
        message: "logout forbidden",
        requestId: "req_logout_forbidden",
      },
    );
  });

  it.each([
    "https://attacker.example/steal",
    "//attacker.example/steal",
    "/../auth/session",
    "/%2e%2e/auth/session",
    "/projects%2Fadmin",
    "/projects%5Cadmin",
    "/projects\\..\\auth/session",
    "/projects#outside",
    "/projects\u0000outside",
  ])("rejects unsafe API path %s before fetching", async (path) => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const client = new APIClient(() => null);

    try {
      await expect(client.get(path)).rejects.toThrow("safe relative API path");
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      fetchSpy.mockRestore();
    }
  });

  it("keeps a safe query inside the API boundary", async () => {
    let requestURL = "not-called";
    server.use(
      http.get("/api/v1/projects", ({ request }) => {
        requestURL = request.url;
        return HttpResponse.json({ projects: [] });
      }),
    );

    const client = new APIClient(() => null);

    await expect(
      client.get("/projects?environment=prod&limit=20"),
    ).resolves.toEqual({ projects: [] });
    expect(new URL(requestURL).search).toBe("?environment=prod&limit=20");
  });

  it("propagates a network failure without inventing response data", async () => {
    server.use(
      http.get("/api/v1/projects", () => HttpResponse.error()),
    );

    const client = new APIClient(() => null);

    await expect(client.get("/projects")).rejects.toBeInstanceOf(TypeError);
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
    const onUnauthorized = vi.fn(() => {
      throw new Error("authentication callback detail");
    });
    const client = new APIClient(
      () => "csrf-token",
      onUnauthorized,
      () => 7,
    );

    await expect(client.get("/projects")).rejects.toMatchObject({
      status: 401,
      code: "invalid_session",
      message: "expired",
    });
    expect(onUnauthorized).toHaveBeenCalledOnce();
    expect(onUnauthorized).toHaveBeenCalledWith(7);
  });
});
