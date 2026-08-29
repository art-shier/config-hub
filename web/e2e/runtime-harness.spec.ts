import { createServer as createHTTPServer } from "node:http";
import { createServer as createTCPServer, type Server } from "node:net";
import { expect, test } from "@playwright/test";
import {
  AsyncCleanupStack,
  retryWithLoopbackPort,
  spawnManagedChild,
  stopChild,
  unusedLoopbackPort,
  waitForReady,
} from "./runtime-harness";

test("cleanup stack releases every resource in reverse order after a failure", async () => {
  const events: string[] = [];
  const cleanups = new AsyncCleanupStack();
  cleanups.defer(async () => { events.push("temporary directory"); });
  cleanups.defer(async () => {
    events.push("proxy");
    throw new Error("proxy close failed");
  });
  cleanups.defer(async () => { events.push("child process"); });

  await expect(cleanups.dispose()).rejects.toThrow("resource cleanup failed");
  expect(events).toEqual(["child process", "proxy", "temporary directory"]);
  await expect(cleanups.dispose()).resolves.toBeUndefined();
  expect(events).toHaveLength(3);
});

test("managed child reports spawn errors", async () => {
  await expect(spawnManagedChild("confighub-command-that-does-not-exist", [])).rejects.toThrow("could not start child process");
});

test("child shutdown escalates to SIGKILL and waits for close and stdio", async () => {
  const child = await spawnManagedChild(process.execPath, [
    "-e",
    "process.on('SIGTERM',()=>process.stdout.write('TERM_HANDLED\\n'));process.stdout.write('READY\\n');setInterval(()=>{},1000)",
  ]);
  let output = "";
  child.child.stdout?.on("data", (chunk: Buffer) => { output += chunk.toString("utf8"); });
  await expect.poll(() => output).toContain("READY");

  await stopChild(child, 50);

  expect(child.child.signalCode).toBe("SIGKILL");
  expect(output).toContain("TERM_HANDLED");
  await expect(child.closed).resolves.toMatchObject({ signal: "SIGKILL" });
});

test("readiness aborts each hanging request within the overall deadline", async () => {
  let requests = 0;
  const server = createHTTPServer((_request, _response) => { requests += 1; });
  await listen(server, 0);
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("test HTTP server did not bind");
  const startedAt = Date.now();
  try {
    await expect(waitForReady(`http://127.0.0.1:${address.port}`, {
      overallTimeoutMs: 220,
      requestTimeoutMs: 50,
      retryDelayMs: 5,
    })).rejects.toThrow("readiness timed out");
  } finally {
    server.closeAllConnections();
    await close(server);
  }
  expect(Date.now() - startedAt).toBeLessThan(1_000);
  expect(requests).toBeGreaterThan(1);
});

test("readiness stops immediately when the child exits", async () => {
  const child = await spawnManagedChild(process.execPath, ["-e", "setTimeout(()=>process.exit(7),25)"]);
  const startedAt = Date.now();
  await expect(waitForReady("http://127.0.0.1:1", {
    child,
    overallTimeoutMs: 5_000,
    requestTimeoutMs: 200,
    retryDelayMs: 5,
  })).rejects.toThrow("child process exited before readiness");
  await stopChild(child, 50);
  expect(Date.now() - startedAt).toBeLessThan(1_000);
});

test("loopback bind retries after a real occupied candidate port", async () => {
  const occupied = createTCPServer();
  await listen(occupied, 0);
  const occupiedAddress = occupied.address();
  if (occupiedAddress === null || typeof occupiedAddress === "string") throw new Error("occupied server did not bind");

  let candidates = 0;
  let bound: Server | undefined;
  try {
    bound = await retryWithLoopbackPort(async (port) => {
      const candidate = createTCPServer();
      await listen(candidate, port);
      return candidate;
    }, {
      attempts: 3,
      candidate: async () => {
        candidates += 1;
        return candidates === 1 ? occupiedAddress.port : unusedLoopbackPort();
      },
    });
    expect(candidates).toBe(2);
  } finally {
    if (bound) await close(bound);
    await close(occupied);
  }
});

async function listen(server: Server, port: number): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => resolve());
  });
}

async function close(server: Server): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
  });
}
