import { expect, test } from "@playwright/test";
test("shows advanced loop hook events in the dark shadcn timeline", async ({ page }) => {
  await page.addInitScript((token)=>localStorage.setItem("ultra-token",token),process.env.ULTRA_TOKEN??"tok-alice");
  await page.goto("/");
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.getByLabel("New session title").fill("Advanced loop");
  await page.getByRole("button",{name:"+"}).click();
  await expect(page.getByLabel("Prompt")).toBeVisible();
  await expect(page.getByTestId("timeline")).toBeVisible();
});
