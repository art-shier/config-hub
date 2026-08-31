import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll } from "vitest";
import { changeLocale } from "../i18n";

export const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(async () => {
  cleanup();
  server.resetHandlers();
  window.history.replaceState(null, "", "/");
  await changeLocale("en-US");
  localStorage.removeItem("confighub.locale");
  document.documentElement.lang = "en-US";
});

afterAll(() => {
  server.close();
});
