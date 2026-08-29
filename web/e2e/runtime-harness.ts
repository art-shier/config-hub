import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { createServer as createTCPServer } from "node:net";

export interface ChildClose {
  code: number | null;
  signal: NodeJS.Signals | null;
}

export interface ManagedChild {
  child: ChildProcess;
  closed: Promise<ChildClose>;
}

type AsyncCleanup = () => Promise<void> | void;

export class AsyncCleanupStack {
  private cleanups: AsyncCleanup[] = [];
  private disposed = false;

  defer(cleanup: AsyncCleanup): void {
    if (this.disposed) throw new Error("cleanup stack already disposed");
    this.cleanups.push(cleanup);
  }

  async dispose(): Promise<void> {
    if (this.disposed) return;
    this.disposed = true;
    const errors: unknown[] = [];
    for (const cleanup of this.cleanups.reverse()) {
      try {
        await cleanup();
      } catch (error) {
        errors.push(error);
      }
    }
    this.cleanups = [];
    if (errors.length > 0) {
      throw new Error("resource cleanup failed", { cause: new AggregateError(errors) });
    }
  }
}

export async function spawnManagedChild(command: string, args: string[], options: SpawnOptions = {}): Promise<ManagedChild> {
  const child = spawn(command, args, options);
  const closed = new Promise<ChildClose>((resolveClose) => {
    child.once("close", (code, signal) => resolveClose({ code, signal }));
  });
  await new Promise<void>((resolveSpawn, rejectSpawn) => {
    child.once("spawn", resolveSpawn);
    child.once("error", () => rejectSpawn(new Error("could not start child process")));
  });
  return { child, closed };
}

export async function stopChild(process: ManagedChild, gracefulTimeoutMs = 15_000): Promise<void> {
  const { child, closed } = process;
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGTERM");
  }
  if (await settlesWithin(closed, gracefulTimeoutMs)) return;
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
  }
  await closed;
}

export interface WaitForReadyOptions {
  overallTimeoutMs?: number;
  requestTimeoutMs?: number;
  retryDelayMs?: number;
  child?: ManagedChild;
}

export class ChildExitedBeforeReadyError extends Error {
  constructor() {
    super("child process exited before readiness");
    this.name = "ChildExitedBeforeReadyError";
  }
}

export async function waitForReady(baseURL: string, options: WaitForReadyOptions = {}): Promise<void> {
  const overallTimeoutMs = options.overallTimeoutMs ?? 30_000;
  const requestTimeoutMs = options.requestTimeoutMs ?? 1_000;
  const retryDelayMs = options.retryDelayMs ?? 50;
  const deadline = Date.now() + overallTimeoutMs;

  while (Date.now() < deadline) {
    const remaining = deadline - Date.now();
    const controller = new AbortController();
    const requestTimer = setTimeout(() => controller.abort(), Math.min(requestTimeoutMs, remaining));
    try {
      const readiness = fetch(`${baseURL}/api/v1/health/ready`, { signal: controller.signal });
      const response = options.child
        ? await Promise.race([
          readiness,
          options.child.closed.then(() => { throw new ChildExitedBeforeReadyError(); }),
        ])
        : await readiness;
      await response.body?.cancel();
      if (response.ok) return;
    } catch (error) {
      if (error instanceof ChildExitedBeforeReadyError) throw error;
      // Connection failures and per-request aborts are expected until the real server listens.
    } finally {
      clearTimeout(requestTimer);
      controller.abort();
    }

    const delay = Math.min(retryDelayMs, Math.max(0, deadline - Date.now()));
    if (delay > 0) await new Promise((resolveDelay) => setTimeout(resolveDelay, delay));
  }
  throw new Error("readiness timed out");
}

export interface LoopbackRetryOptions {
  attempts?: number;
  candidate?: () => Promise<number>;
  retryIf?: (error: unknown) => boolean;
}

export async function retryWithLoopbackPort<T>(
  start: (port: number) => Promise<T>,
  options: LoopbackRetryOptions = {},
): Promise<T> {
  const attempts = options.attempts ?? 5;
  const candidate = options.candidate ?? unusedLoopbackPort;
  let lastError: unknown;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      return await start(await candidate());
    } catch (error) {
      lastError = error;
      if (options.retryIf && !options.retryIf(error)) throw error;
    }
  }
  throw new Error(`loopback startup failed after ${attempts} attempts`, { cause: lastError });
}

export async function unusedLoopbackPort(): Promise<number> {
  const server = createTCPServer();
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(0, "127.0.0.1", resolveListen);
  });
  const address = server.address();
  if (address === null || typeof address === "string") {
    await closeTCPServer(server);
    throw new Error("could not reserve loopback port");
  }
  await closeTCPServer(server);
  return address.port;
}

async function closeTCPServer(server: ReturnType<typeof createTCPServer>): Promise<void> {
  await new Promise<void>((resolveClose, rejectClose) => {
    server.close((error) => error ? rejectClose(error) : resolveClose());
  });
}

async function settlesWithin<T>(promise: Promise<T>, timeoutMs: number): Promise<boolean> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timedOut = new Promise<false>((resolveTimeout) => {
    timer = setTimeout(() => resolveTimeout(false), timeoutMs);
  });
  const settled = promise.then(() => true);
  const result = await Promise.race([settled, timedOut]);
  if (timer !== undefined) clearTimeout(timer);
  return result;
}
