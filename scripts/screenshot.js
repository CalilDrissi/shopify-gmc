// scripts/screenshot.js — drives a headed Chromium against /_components
// and snapshots both themes. Run via: xvfb-run -a node scripts/screenshot.js
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const URL = process.env.URL || 'http://localhost:8080/_components';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: false });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 1800 },
    deviceScaleFactor: 1,
  });
  const page = await context.newPage();
  await page.goto(URL, { waitUntil: 'networkidle' });

  // Force the data-theme attribute so we get a deterministic light shot.
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
  await page.screenshot({ path: path.join(OUT, 'components-light.png'), fullPage: true });
  console.log('saved components-light.png');

  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
  await page.screenshot({ path: path.join(OUT, 'components-dark.png'), fullPage: true });
  console.log('saved components-dark.png');

  // Hover the first filled button so the state-layer shows up.
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
  const filled = page.locator('button.c-button--filled').first();
  await filled.hover();
  await page.waitForTimeout(150);
  await page.locator('section').first().screenshot({ path: path.join(OUT, 'buttons-hover.png') });
  console.log('saved buttons-hover.png');

  // Focus an input to capture the focus ring on the form-field section.
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
  await page.locator('#ff-email').focus();
  const formSection = page.locator('section').nth(3);
  await formSection.screenshot({ path: path.join(OUT, 'form-field-focus.png') });
  console.log('saved form-field-focus.png');

  await browser.close();
})().catch(err => {
  console.error(err);
  process.exit(1);
});
