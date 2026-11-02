import { test as base, expect, type Locator, type Page } from "@playwright/test";
import { FakeRuntime } from "./fake/server";

export { expect };

export const test = base.extend<{ fake: FakeRuntime }>({
  fake: async ({}, use) => {
    const fake = await new FakeRuntime().start();
    await use(fake);
    await fake.stop();
  },
});

/** Open a path with a fresh one-time bootstrap code and wait for the live stream. */
export async function open(page: Page, fake: FakeRuntime, pathname = "/"): Promise<void> {
  await page.goto(fake.bootstrapUrl(pathname));
  await expect(page.getByTestId("runtime-status")).toHaveText("런타임 실시간 연결");
}

export async function openProject(page: Page, fake: FakeRuntime, projectId: string, query = ""): Promise<void> {
  await open(page, fake, `/projects/${encodeURIComponent(projectId)}${query}`);
  await expect(page.getByTestId("project-version")).toHaveText(`project_version ${fake.project(projectId).snap.project_version}`);
  await expect(page.getByTestId("connection-banner")).toHaveCount(0);
}

export function node(page: Page, issueId: string): Locator {
  return page.locator(`.react-flow__node[data-id="issue:${issueId}"]`);
}

/** The page shows the same project_version as the server projection. */
export async function expectVersion(page: Page, fake: FakeRuntime, projectId: string): Promise<void> {
  await expect(page.getByTestId("project-version")).toHaveText(`project_version ${fake.project(projectId).snap.project_version}`);
}
