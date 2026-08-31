import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { request as requestHTTP } from "node:http";
import { createServer as createHTTPSServer, type Server as HTTPSServer } from "node:https";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { test, expect, type Locator, type Page } from "@playwright/test";
import {
  AsyncCleanupStack,
  ChildExitedBeforeReadyError,
  retryWithLoopbackPort,
  spawnManagedChild,
  stopChild,
  waitForReady,
} from "./runtime-harness";

const executeFile = promisify(execFile);
const adminPassword = "e2e-admin-password";
const developerPassword = "e2e-developer-password";
const originalDatabaseValue = "postgres://e2e-revision-one";
const revisionTwoDatabaseValue = "postgres://e2e-revision-two";
const originalFeatureValue = "e2e-feature-revision-one";
const sessionSigningKey = "e2e-session-key-012345678901234567890123456789012345";
let issuedMachineToken = "";
const observedBrowserCredentials = new Set<string>();

interface E2ERuntime {
  origin: string;
  logs(): string;
  stop(): Promise<void>;
}

type MachineTokenCheck = "format" | "clipboard" | "dismissed" | "reopened";

const machineTokenFailureMessages: Record<MachineTokenCheck, string> = {
  format: "issued machine token had an unexpected format",
  clipboard: "issued machine token was not copied to the clipboard",
  dismissed: "issued machine token remained visible after confirmation",
  reopened: "issued machine token was shown after reopening its metadata",
};

function assertMachineTokenCheck(passed: boolean, check: MachineTokenCheck): void {
  if (!passed) throw new Error(machineTokenFailureMessages[check]);
}

let runtimeServer: E2ERuntime;

test.beforeAll(async () => {
  runtimeServer = await startRuntimeServer();
});

test.afterAll(async () => {
  if (runtimeServer) {
    let stopFailed = false;
    try {
      await runtimeServer.stop();
    } catch {
      stopFailed = true;
    }
    for (const secret of [adminPassword, developerPassword, sessionSigningKey, originalDatabaseValue, revisionTwoDatabaseValue, originalFeatureValue, issuedMachineToken, ...observedBrowserCredentials]) {
      if (secret === "") continue;
      if (runtimeServer.logs().includes(secret)) {
        throw new Error("runtime logs contained browser credentials or configuration values");
      }
    }
    if (stopFailed) throw new Error("runtime resources did not stop cleanly");
  }
});

test("machine-token assertion failures omit the token value", () => {
  const syntheticToken = "synthetic-machine-token-secret";
  const failingChecks: Array<readonly [boolean, MachineTokenCheck]> = [
    [/^ch_[A-Za-z0-9_-]{43}$/u.test(syntheticToken), "format"],
    [`clipboard:${syntheticToken}` === syntheticToken, "clipboard"],
    [[syntheticToken].length === 0, "dismissed"],
    [!`dialog:${syntheticToken}`.includes(syntheticToken), "reopened"],
  ];
  for (const [passed, check] of failingChecks) {
    let failure: unknown;
    try {
      assertMachineTokenCheck(passed, check);
    } catch (error) {
      failure = error;
    }
    if (!(failure instanceof Error)) {
      throw new Error("machine-token assertion did not fail");
    }
    if (failure.message.includes(syntheticToken)) {
      throw new Error("machine-token assertion error disclosed the token value");
    }
  }
});

test("admin completes configuration, conflict, Token, diff, and rollback workflows", async ({ browser, page }) => {
  await login(page);
  await createProject(page);
  await page.getByRole("link", { name: "Shop browser flow" }).click();
  await expect(page.getByRole("heading", { name: "Shop browser flow", level: 1 })).toBeVisible();
  await createEnvironment(page, "development", "Development");
  await createEnvironment(page, "production", "Production");
  await page.getByLabel("Active environment").selectOption("production");

  await page.getByRole("button", { name: "Edit configuration" }).click();
  await page.getByRole("button", { name: "Add entry" }).click();
  await page.getByRole("button", { name: "Add entry" }).click();
  const firstDraft = page.getByRole("group", { name: "Configuration entries" }).locator("fieldset").nth(0);
  const secondDraft = page.getByRole("group", { name: "Configuration entries" }).locator("fieldset").nth(1);
  await firstDraft.getByLabel(/^Key for /).fill("DATABASE_URL");
  await firstDraft.getByLabel(/^Value for /).fill(originalDatabaseValue);
  await firstDraft.getByLabel(/^Service for /).fill("api");
  await secondDraft.getByLabel(/^Key for /).fill("FEATURE_FLAG");
  await secondDraft.getByLabel(/^Value for /).fill(originalFeatureValue);
  await page.getByLabel("Change message").fill("browser revision one");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("status")).toContainText("Revision 1 saved");
  await expect(page.getByTestId("configuration-value-DATABASE_URL")).toHaveText(originalDatabaseValue);

  const secondContext = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 1000 },
  });
  try {
    const secondPage = await secondContext.newPage();
    await login(secondPage);
    await secondPage.goto(`${runtimeServer.origin}/projects/shop?environment=production&tab=configuration`);
    await expect(secondPage.getByRole("heading", { name: "Configuration", exact: true })).toBeVisible();
    await secondPage.getByRole("button", { name: "Edit configuration" }).click();
    await secondPage.getByLabel("Value for DATABASE_URL").fill("postgres://second-context-draft");
    await secondPage.getByLabel("Change message").fill("second context draft");

    await page.getByRole("button", { name: "Edit configuration" }).click();
    await page.getByLabel("Value for DATABASE_URL").fill(revisionTwoDatabaseValue);
    await page.getByLabel("Change message").fill("browser revision two");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByRole("status")).toContainText("Revision 2 saved");

    await secondPage.getByRole("button", { name: "Save changes" }).click();
    await expect(secondPage.getByRole("alert")).toHaveText("Configuration changed since you loaded it");
    await secondPage.getByRole("button", { name: "Refresh and compare" }).click();
    await expect(secondPage.getByRole("heading", { name: "Latest server compared with your draft" })).toBeVisible();
    await expect(secondPage.getByTestId("conflict-server-DATABASE_URL")).toHaveText(revisionTwoDatabaseValue);
    await expect(secondPage.getByTestId("conflict-local-DATABASE_URL")).toHaveText("postgres://second-context-draft");
  } finally {
    await secondContext.close();
  }

  await page.getByRole("tab", { name: "Versions" }).click();
  await expect(page.getByRole("heading", { name: "Versions", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "View version 1" }).click();
  await expect(page.getByRole("heading", { name: "Version 1 to current version 2" })).toBeVisible();
  await expect(page.getByText(originalDatabaseValue, { exact: true })).toBeVisible();
  await expect(page.getByText(revisionTwoDatabaseValue, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Rollback to version 1" }).click();
  await expect(page.getByRole("heading", { name: "Rollback to version 1?" })).toBeVisible();
  await page.getByLabel("Rollback message").fill("browser restore revision one");
  await page.getByRole("button", { name: "Create rollback version" }).click();
  await expect(page.getByRole("heading", { name: "Versions", exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Configuration" }).click();
  await expect(page.getByText("Current register / Version 3")).toBeVisible();
  await expect(page.getByTestId("configuration-value-DATABASE_URL")).toHaveText(originalDatabaseValue);

  await page.getByRole("link", { name: "Machine Access" }).click();
  await expect(page.getByRole("heading", { name: "Machine Access" })).toBeVisible();
  await page.getByRole("button", { name: "New identity" }).click();
  await page.getByLabel("Machine name").fill("shop-browser-ci");
  await page.getByLabel("Description", { exact: true }).fill("browser Token flow");
  await page.getByRole("button", { name: "Create identity" }).click();
  await expect(page.getByRole("heading", { name: "shop-browser-ci" })).toBeVisible();
  await page.getByRole("button", { name: "Issue Token" }).click();
  await page.getByLabel("Token name").fill("browser-token");
  await page.getByRole("dialog").getByRole("button", { name: "Issue Token", exact: true }).click();
  const issuedToken = await page.getByLabel("Issued Token").textContent();
  issuedMachineToken = issuedToken ?? "";
  const tokenHasExpectedFormat = /^ch_[A-Za-z0-9_-]{43}$/u.test(issuedMachineToken);
  assertMachineTokenCheck(tokenHasExpectedFormat, "format");
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: runtimeServer.origin });
  await page.getByRole("button", { name: "Copy Token" }).click();
  await expect.poll(async () => (await page.evaluate(() => navigator.clipboard.readText())) === issuedMachineToken, {
    message: machineTokenFailureMessages.clipboard,
  }).toBe(true);
  await page.getByRole("button", { name: "I have copied it" }).click();
  await expect.poll(async () => (await page.getByLabel("Issued Token").count()) === 0, {
    message: machineTokenFailureMessages.dismissed,
  }).toBe(true);
  await page.getByRole("button", { name: "View browser-token Token" }).click();
  const reopenedDialog = page.getByRole("dialog");
  await expect(reopenedDialog).toBeVisible();
  const reopenedDialogText = await reopenedDialog.textContent() ?? "";
  const reopenedDialogOmitsToken = !reopenedDialogText.includes(issuedMachineToken);
  assertMachineTokenCheck(reopenedDialogOmitsToken, "reopened");
});

test("locale follows Chinese browser preference and persists an explicit English choice", async ({ browser }) => {
  const context = await browser.newContext({
    locale: "zh-CN",
    ignoreHTTPSErrors: true,
    viewport: { width: 390, height: 844 },
  });
  try {
    const page = await context.newPage();
    await page.goto(`${runtimeServer.origin}/login`);

    await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
    await expect(page.getByRole("button", { name: "登录" })).toBeVisible();
    await page.getByRole("combobox", { name: "语言" }).selectOption("en-US");
    await expect(page.locator("html")).toHaveAttribute("lang", "en-US");

    await page.reload();

    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => localStorage.getItem("confighub.locale")))
      .toBe("en-US");
  } finally {
    await context.close();
  }
});

test("Chinese authenticated narrow workflow preserves route and form value across keyboard locale switches", async ({ browser }) => {
  const context = await browser.newContext({
    locale: "zh-CN",
    ignoreHTTPSErrors: true,
    reducedMotion: "reduce",
    viewport: { width: 390, height: 844 },
  });
  try {
    const page = await context.newPage();
    await loginInChinese(page);

    const headerLanguage = page.locator(".app-header .language-switcher select");
    const navigationButton = page.getByRole("button", { name: "打开导航" });
    const signOutButton = page.getByRole("button", { name: "退出登录" });
    await expectNonOverlapping([
      { name: "language selector", locator: headerLanguage },
      { name: "navigation button", locator: navigationButton },
      { name: "sign-out button", locator: signOutButton },
    ], 390);

    await navigationButton.focus();
    await navigationButton.press("Enter");
    const projectsLink = page.getByRole("link", { name: "项目" });
    await expect(projectsLink).toBeFocused();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Tab");
    await page.keyboard.press("Tab");
    const systemLink = page.getByRole("link", { name: "系统" });
    await expect(systemLink).toBeFocused();
    await systemLink.press("Enter");
    await expect(page).toHaveURL(`${runtimeServer.origin}/system`);
    await expect(page.getByRole("heading", { name: "系统", level: 1 })).toBeVisible();

    const systemURL = page.url();
    await headerLanguage.focus();
    await headerLanguage.press("ArrowUp");
    await expect(headerLanguage).toHaveValue("en-US");
    await expect(headerLanguage).toHaveAccessibleName("Language");
    await expect(headerLanguage).toBeFocused();
    await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
    await expect(page).toHaveURL(systemURL);
    await expect(page.getByRole("heading", { name: "System", level: 1 })).toBeVisible();

    await headerLanguage.press("ArrowDown");
    await expect(headerLanguage).toHaveValue("zh-CN");
    await expect(headerLanguage).toHaveAccessibleName("语言");
    await expect(headerLanguage).toBeFocused();
    await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
    await expect(page).toHaveURL(systemURL);

    const machineNavigationButton = page.getByRole("button", { name: "打开导航" });
    await machineNavigationButton.focus();
    await machineNavigationButton.press("Enter");
    const projectsLinkFromSystem = page.getByRole("link", { name: "项目" });
    await expect(projectsLinkFromSystem).toBeFocused();
    await page.keyboard.press("Tab");
    const machineAccessLink = page.getByRole("link", { name: "机器访问" });
    await expect(machineAccessLink).toBeFocused();
    await machineAccessLink.press("Enter");
    await expect(page).toHaveURL(`${runtimeServer.origin}/machine-access`);
    await expect(page.getByRole("heading", { name: "机器访问", level: 1 })).toBeVisible();

    const newIdentityButton = page.getByRole("button", { name: "新建身份" });
    await newIdentityButton.focus();
    await newIdentityButton.press("Enter");
    const identityDialog = page.getByRole("dialog", { name: "新建机器身份" });
    await identityDialog.getByLabel("机器名称").fill("locale-browser-agent");
    await identityDialog.getByLabel("描述").fill("初始身份说明");
    await identityDialog.getByRole("button", { name: "创建身份" }).press("Enter");
    await expect(page.getByRole("heading", { name: "locale-browser-agent" })).toBeVisible();

    const identityForm = page.locator(".machine-identity-form");
    const identityDescription = identityForm.locator("textarea");
    await expect(identityDescription).toHaveAccessibleName("描述");
    const exactDraft = "语言切换草稿 / DATABASE_URL";
    await identityDescription.fill(exactDraft);
    const machineAccessURL = page.url();

    await headerLanguage.focus();
    await headerLanguage.press("ArrowUp");
    await expect(headerLanguage).toHaveValue("en-US");
    await expect(headerLanguage).toBeFocused();
    await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
    await expect(page).toHaveURL(machineAccessURL);
    await expect(identityDescription).toHaveValue(exactDraft);
    await expect(identityDescription).toHaveAccessibleName("Description");
    await expect(page.getByRole("heading", { name: "Machine Access", level: 1 })).toBeVisible();

    await headerLanguage.press("ArrowDown");
    await expect(headerLanguage).toHaveValue("zh-CN");
    await expect(headerLanguage).toBeFocused();
    await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
    await expect(page).toHaveURL(machineAccessURL);
    await expect(identityDescription).toHaveValue(exactDraft);
    await expect(identityDescription).toHaveAccessibleName("描述");
  } finally {
    await context.close();
  }
});

async function login(page: Page): Promise<void> {
  await page.goto(`${runtimeServer.origin}/login`);
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(adminPassword);
  const [loginResponse] = await Promise.all([
    page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/auth/login" && response.request().method() === "POST"),
    page.getByRole("button", { name: "Sign in" }).click(),
  ]);
  const sessionCookie = (await page.context().cookies(runtimeServer.origin)).find((cookie) => cookie.name === "confighub_session");
  if (sessionCookie?.value) observedBrowserCredentials.add(sessionCookie.value);
  const loginPayload = await loginResponse.json() as { csrf_token?: unknown };
  if (typeof loginPayload.csrf_token === "string" && loginPayload.csrf_token !== "") {
    observedBrowserCredentials.add(loginPayload.csrf_token);
  }
  await expect(page).toHaveURL(`${runtimeServer.origin}/projects`);
  await expect(page.getByRole("heading", { name: "Projects", exact: true })).toBeVisible();
}

async function loginInChinese(page: Page): Promise<void> {
  await page.goto(`${runtimeServer.origin}/login`);
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await page.getByLabel("用户名").fill("admin");
  await page.getByLabel("密码").fill(adminPassword);
  const [loginResponse] = await Promise.all([
    page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/auth/login" && response.request().method() === "POST"),
    page.getByRole("button", { name: "登录" }).click(),
  ]);
  const sessionCookie = (await page.context().cookies(runtimeServer.origin)).find((cookie) => cookie.name === "confighub_session");
  if (sessionCookie?.value) observedBrowserCredentials.add(sessionCookie.value);
  const loginPayload = await loginResponse.json() as { csrf_token?: unknown };
  if (typeof loginPayload.csrf_token === "string" && loginPayload.csrf_token !== "") {
    observedBrowserCredentials.add(loginPayload.csrf_token);
  }
  await expect(page).toHaveURL(`${runtimeServer.origin}/projects`);
  await expect(page.getByRole("heading", { name: "项目", exact: true })).toBeVisible();
}

async function expectNonOverlapping(
  controls: Array<{ name: string; locator: Locator }>,
  viewportWidth: number,
): Promise<void> {
  const boxes = await Promise.all(controls.map(async ({ name, locator }) => {
    await expect(locator).toBeVisible();
    const box = await locator.boundingBox();
    if (box === null) throw new Error(`${name} did not expose a bounding box`);
    expect(box.x, `${name} starts outside the viewport`).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, `${name} extends beyond the viewport`).toBeLessThanOrEqual(viewportWidth);
    return { name, box };
  }));

  for (let leftIndex = 0; leftIndex < boxes.length; leftIndex += 1) {
    for (let rightIndex = leftIndex + 1; rightIndex < boxes.length; rightIndex += 1) {
      const left = boxes[leftIndex];
      const right = boxes[rightIndex];
      const overlaps =
        left.box.x < right.box.x + right.box.width &&
        left.box.x + left.box.width > right.box.x &&
        left.box.y < right.box.y + right.box.height &&
        left.box.y + left.box.height > right.box.y;
      expect(overlaps, `${left.name} overlaps ${right.name}`).toBe(false);
    }
  }
}

async function createProject(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New project" }).click();
  await page.getByLabel("Project slug").fill("shop");
  await page.getByLabel("Project name").fill("Shop browser flow");
  await page.getByLabel("Description").fill("Playwright real service workflow");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("link", { name: "Shop browser flow" })).toBeVisible();
}

async function createEnvironment(page: Page, slug: string, name: string): Promise<void> {
  await page.getByRole("button", { name: "New environment" }).click();
  await page.getByLabel("Environment slug").fill(slug);
  await page.getByLabel("Environment name").fill(name);
  await page.getByRole("button", { name: "Create environment" }).click();
  await expect(page.getByRole("option", { name })).toBeAttached();
}

async function startRuntimeServer(): Promise<E2ERuntime> {
  const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
  const cleanups = new AsyncCleanupStack();
  let serverLogs = "";
  try {
    const runtimeDirectory = await mkdtemp(join(tmpdir(), "confighub-playwright-"));
    cleanups.defer(async () => { await rm(runtimeDirectory, { recursive: true, force: true }); });
    const serverBinary = join(runtimeDirectory, "confighub-server");
    const usersPath = join(runtimeDirectory, "users.yaml");
    const sessionKeyPath = join(runtimeDirectory, "session.key");
    const certificatePath = join(runtimeDirectory, "localhost.crt");
    const privateKeyPath = join(runtimeDirectory, "localhost.key");

    await executeFile("go", ["build", "-o", serverBinary, "./cmd/server"], { cwd: repositoryRoot });
    await executeFile("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-sha256", "-nodes", "-days", "1",
      "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1",
      "-keyout", privateKeyPath, "-out", certificatePath,
    ]);

    let backendPort = 0;
    const proxy = createHTTPSServer({
      key: await readFile(privateKeyPath),
      cert: await readFile(certificatePath),
    }, (request, response) => {
      const upstream = requestHTTP({
        hostname: "127.0.0.1",
        port: backendPort,
        path: request.url,
        method: request.method,
        headers: request.headers,
      }, (upstreamResponse) => {
        response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
        upstreamResponse.pipe(response);
      });
      upstream.on("error", () => {
        if (!response.headersSent) response.writeHead(502);
        response.end();
      });
      request.pipe(upstream);
    });
    await listenHTTPS(proxy);
    cleanups.defer(async () => { await closeHTTPS(proxy); });
    const proxyAddress = proxy.address();
    if (proxyAddress === null || typeof proxyAddress === "string") {
      throw new Error("HTTPS proxy did not bind a TCP address");
    }
    const origin = `https://127.0.0.1:${proxyAddress.port}`;

    await writeRestricted(usersPath, `users:
  - username: admin
    display_name: E2E Administrator
    password: ${adminPassword}
    role: admin
    enabled: true
  - username: developer-a
    display_name: E2E Developer
    password: ${developerPassword}
    role: member
    enabled: true
`);
    await writeRestricted(sessionKeyPath, `${sessionSigningKey}\n`);

    let runtimeAttempt = 0;
    const server = await retryWithLoopbackPort(async (candidatePort) => {
      runtimeAttempt += 1;
      backendPort = candidatePort;
      const configPath = join(runtimeDirectory, `config-${runtimeAttempt}.yaml`);
      const databasePath = join(runtimeDirectory, `data-${runtimeAttempt}`, "confighub.db");
      await writeRestricted(configPath, `server:
  listen: 127.0.0.1:${backendPort}
  public_url: ${origin}
  trusted_proxy_cidrs:
    - 127.0.0.1/32
database:
  path: ${databasePath}
auth:
  users_file: ${usersPath}
  session_key_file: ${sessionKeyPath}
  session_ttl: 1h
backup:
  directory: ${join(runtimeDirectory, "backups")}
`);
      const child = await spawnManagedChild(serverBinary, ["serve", "--config", configPath], {
        cwd: repositoryRoot,
        stdio: ["ignore", "pipe", "pipe"],
      });
      child.child.stdout?.on("data", (chunk: Buffer) => { serverLogs += chunk.toString("utf8"); });
      child.child.stderr?.on("data", (chunk: Buffer) => { serverLogs += chunk.toString("utf8"); });
      try {
        await waitForReady(`http://127.0.0.1:${backendPort}`, { child });
        return child;
      } catch (error) {
        await stopChild(child);
        if (error instanceof ChildExitedBeforeReadyError) throw error;
        throw new Error("runtime readiness failed without a bind error");
      }
    }, { retryIf: (error) => error instanceof ChildExitedBeforeReadyError });
    cleanups.defer(async () => { await stopChild(server); });

    let stopped = false;
    return {
      origin,
      logs: () => serverLogs,
      stop: async () => {
        if (stopped) return;
        stopped = true;
        await cleanups.dispose();
      },
    };
  } catch {
    let cleanupFailed = false;
    try {
      await cleanups.dispose();
    } catch {
      cleanupFailed = true;
    }
    for (const secret of [adminPassword, developerPassword, sessionSigningKey, originalDatabaseValue, revisionTwoDatabaseValue, originalFeatureValue]) {
      if (serverLogs.includes(secret)) {
        throw new Error("runtime startup logs contained a protected value");
      }
    }
    if (cleanupFailed) throw new Error("runtime startup cleanup failed");
    throw new Error("real server did not become ready");
  }
}

async function writeRestricted(path: string, contents: string): Promise<void> {
  await writeFile(path, contents, { encoding: "utf8", mode: 0o600, flag: "wx" });
}

async function listenHTTPS(server: HTTPSServer): Promise<void> {
  await new Promise<void>((resolveListen, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolveListen());
  });
}

async function closeHTTPS(server: HTTPSServer): Promise<void> {
  await new Promise<void>((resolveClose, rejectClose) => {
    server.close((error) => error ? rejectClose(error) : resolveClose());
    server.closeAllConnections();
  });
}
