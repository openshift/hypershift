#!/usr/bin/env node
/**
 * Visual comparison of two built doc sites.
 *
 * Usage:
 *   node compare.mjs <old-site-dir> <new-site-dir> [--output <dir>]
 *
 * Serves both sites locally, screenshots every page with Playwright,
 * runs pixel-level diffs, and generates an HTML report.
 */

import { chromium } from "playwright";
import {
  readFileSync, writeFileSync, mkdirSync, rmSync, existsSync,
  readdirSync, statSync,
} from "fs";
import { join, relative, resolve } from "path";
import { createServer } from "http";
import { readFile } from "fs/promises";
import { extname } from "path";
import { PNG } from "pngjs";
import pixelmatch from "pixelmatch";

const VIEWPORT = { width: 1280, height: 900 };
const DIFF_THRESHOLD = 0.1;
const CONCURRENCY = 4;
const MAX_PNG_BYTES = 5 * 1024 * 1024;

function parseArgs() {
  const args = process.argv.slice(2);
  let outputDir = null;
  const positional = [];

  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--output" || args[i] === "-o") {
      outputDir = args[++i];
    } else if (!args[i].startsWith("-")) {
      positional.push(args[i]);
    }
  }

  if (positional.length < 2) {
    console.error("Usage: node compare.mjs <old-site-dir> <new-site-dir> [--output <dir>]");
    process.exit(1);
  }

  return {
    oldSite: resolve(positional[0]),
    newSite: resolve(positional[1]),
    outputDir: resolve(outputDir || "compare-output"),
  };
}

function collectPages(siteDir) {
  const pages = [];
  function walk(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        walk(join(dir, entry.name));
      } else if (entry.name === "index.html") {
        const rel = relative(siteDir, join(dir, entry.name));
        pages.push("/" + rel.replace(/index\.html$/, ""));
      }
    }
  }
  walk(siteDir);
  return pages.sort();
}

function routeToFilename(route) {
  if (route === "/") return "_root_.png";
  return route.replace(/^\//, "").replace(/\/$/, "").replace(/\//g, "__") + ".png";
}

async function startServer(siteDir, port) {
  const mimeTypes = {
    ".html": "text/html", ".css": "text/css",
    ".js": "application/javascript", ".png": "image/png",
    ".jpg": "image/jpeg", ".svg": "image/svg+xml",
    ".json": "application/json", ".woff2": "font/woff2",
    ".woff": "font/woff", ".ttf": "font/ttf",
  };

  const root = resolve(siteDir);
  const server = createServer(async (req, res) => {
    let filePath = resolve(root, (req.url === "/" ? "index.html" : req.url).replace(/^\//, ""));
    if (!filePath.startsWith(root)) {
      res.writeHead(403);
      res.end("Forbidden");
      return;
    }
    if (!filePath.endsWith(".html") && existsSync(join(filePath, "index.html"))) {
      filePath = join(filePath, "index.html");
    }
    try {
      const data = await readFile(filePath);
      res.writeHead(200, { "Content-Type": mimeTypes[extname(filePath)] || "application/octet-stream" });
      res.end(data);
    } catch {
      res.writeHead(404);
      res.end("Not found");
    }
  });

  return new Promise((resolve) => {
    server.listen(port, "127.0.0.1", () => resolve(server));
  });
}

async function screenshotPages(browser, baseUrl, pages, outDir) {
  mkdirSync(outDir, { recursive: true });
  const failed = [];

  async function worker(queue) {
    const context = await browser.newContext({ viewport: VIEWPORT });
    const page = await context.newPage();
    while (queue.length > 0) {
      const route = queue.shift();
      const url = baseUrl + route;
      try {
        await page.goto(url, { waitUntil: "networkidle", timeout: 30000 });
        await page.waitForTimeout(1000);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({
          path: join(outDir, routeToFilename(route)),
          fullPage: true,
        });
      } catch (err) {
        console.error(`  FAIL ${route}: ${err.message}`);
        failed.push(route);
      }
    }
    await context.close();
  }

  const queue = [...pages];
  await Promise.all(Array.from({ length: CONCURRENCY }, () => worker(queue)));
  return failed;
}

function diffScreenshots(oldDir, newDir, diffDir) {
  mkdirSync(diffDir, { recursive: true });
  const results = [];

  const oldFiles = new Set(readdirSync(oldDir));
  const newFiles = new Set(readdirSync(newDir));
  const allFiles = [...new Set([...oldFiles, ...newFiles])].sort();

  for (const file of allFiles) {
    if (!file.endsWith(".png")) continue;

    const pageName = file.replace(/__/g, "/").replace(/\.png$/, "").replace("_root_", "/");
    const oldPath = join(oldDir, file);
    const newPath = join(newDir, file);

    if (!oldFiles.has(file)) {
      results.push({ page: pageName, status: "new-only", diffPixels: 0, diffPercent: 0 });
      continue;
    }
    if (!newFiles.has(file)) {
      results.push({ page: pageName, status: "old-only", diffPixels: 0, diffPercent: 0 });
      continue;
    }

    const oldSize = statSync(oldPath).size;
    const newSize = statSync(newPath).size;
    if (oldSize > MAX_PNG_BYTES || newSize > MAX_PNG_BYTES) {
      console.log(`  SKIP ${pageName} (${Math.round(Math.max(oldSize, newSize) / 1024 / 1024)}MB)`);
      results.push({ page: pageName, status: "skipped-too-large", diffPixels: 0, diffPercent: 0 });
      continue;
    }

    try {
      const oldImg = PNG.sync.read(readFileSync(oldPath));
      const newImg = PNG.sync.read(readFileSync(newPath));

      const width = Math.max(oldImg.width, newImg.width);
      const height = Math.max(oldImg.height, newImg.height);

      const padded = (img) => {
        if (img.width === width && img.height === height) return img;
        const p = new PNG({ width, height });
        PNG.bitblt(img, p, 0, 0, img.width, img.height, 0, 0);
        return p;
      };

      const a = padded(oldImg);
      const b = padded(newImg);
      const diff = new PNG({ width, height });

      const diffPixels = pixelmatch(a.data, b.data, diff.data, width, height, {
        threshold: DIFF_THRESHOLD,
      });
      const totalPixels = width * height;
      const diffPercent = ((diffPixels / totalPixels) * 100).toFixed(2);

      if (diffPixels > 0) {
        writeFileSync(join(diffDir, file), PNG.sync.write(diff));
      }

      a.data = null;
      b.data = null;
      diff.data = null;

      results.push({
        page: pageName,
        status: diffPixels === 0 ? "identical" : "changed",
        diffPixels,
        diffPercent: parseFloat(diffPercent),
      });
    } catch (err) {
      console.error(`  ERROR ${pageName}: ${err.message}`);
      results.push({ page: pageName, status: "error", diffPixels: 0, diffPercent: 0 });
    }
  }

  return results;
}

function generateReport(results, outputDir) {
  const changed = results.filter((r) => r.status === "changed").sort((a, b) => b.diffPercent - a.diffPercent);
  const identical = results.filter((r) => r.status === "identical");
  const oldOnly = results.filter((r) => r.status === "old-only");
  const newOnly = results.filter((r) => r.status === "new-only");
  const skipped = results.filter((r) => r.status === "skipped-too-large");
  const failed = results.filter((r) => r.status === "failed");

  console.log(`\n=== Results ===`);
  console.log(`Identical: ${identical.length}`);
  console.log(`Changed:   ${changed.length}`);
  console.log(`Old only:  ${oldOnly.length}`);
  console.log(`New only:  ${newOnly.length}`);
  console.log(`Skipped:   ${skipped.length}`);
  if (failed.length) console.log(`Failed:    ${failed.length}`);

  if (oldOnly.length) {
    console.log(`\nMissing from new site:`);
    for (const r of oldOnly) console.log(`  ${r.page}`);
  }
  if (changed.length) {
    console.log(`\nChanged (by diff %):`);
    for (const r of changed) console.log(`  ${r.diffPercent}%  ${r.page}`);
  }

  writeFileSync(join(outputDir, "results.json"), JSON.stringify(results, null, 2));

  const pageToFile = (page) => {
    if (page === "/") return "_root_.png";
    return page.replace(/^\//, "").replace(/\/$/, "").replace(/\//g, "__") + ".png";
  };

  let html = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Docs Visual Comparison</title>
<style>
body{font-family:system-ui;max-width:1400px;margin:0 auto;padding:20px;background:#1a1a1a;color:#e0e0e0}
h1,h2,h3,h4{color:#fff}
.summary{display:flex;gap:20px;margin-bottom:20px;flex-wrap:wrap}
.stat{padding:15px 25px;border-radius:8px;font-size:1.2em}
.stat.identical{background:#1a3a1a;color:#4caf50}
.stat.changed{background:#3a2a1a;color:#ff9800}
.stat.missing{background:#3a1a1a;color:#f44336}
.stat.new{background:#1a2a3a;color:#2196f3}
.stat.skipped{background:#2a2a2a;color:#999}
.page{border:1px solid #333;margin:15px 0;border-radius:8px;overflow:hidden}
.page summary{padding:10px 15px;background:#252525;cursor:pointer;display:flex;justify-content:space-between;align-items:center;list-style:none}
.page summary::-webkit-details-marker{display:none}
.page summary:hover{background:#303030}
.page-body{padding:10px}
.compare{display:flex;gap:10px;overflow-x:auto}
.compare>div{flex:1;min-width:0}
.compare img{width:100%;border:1px solid #444}
.diff-img img{max-width:90%;border:2px solid #f44336}
.badge{padding:2px 8px;border-radius:4px;font-size:.85em}
.badge.high{background:#f44336;color:#fff}
.badge.medium{background:#ff9800;color:#000}
.badge.low{background:#4caf50;color:#fff}
</style></head><body>
<h1>Documentation Visual Comparison</h1>
<div class="summary">
<div class="stat identical">${identical.length} identical</div>
<div class="stat changed">${changed.length} changed</div>
<div class="stat missing">${oldOnly.length} missing</div>
<div class="stat new">${newOnly.length} new</div>
<div class="stat skipped">${skipped.length} skipped</div>
${failed.length ? `<div class="stat missing">${failed.length} failed</div>` : ""}
</div>`;

  if (failed.length) {
    html += `<h2>Failed to screenshot</h2>`;
    for (const r of failed) html += `<div class="page"><div class="page-header">${r.page}</div></div>`;
  }

  if (oldOnly.length) {
    html += `<h2>Missing from new site</h2>`;
    for (const r of oldOnly) html += `<div class="page"><div class="page-header">${r.page}</div></div>`;
  }
  if (newOnly.length) {
    html += `<h2>Only in new site</h2>`;
    for (const r of newOnly) html += `<div class="page"><div class="page-header">${r.page}</div></div>`;
  }

  if (changed.length) {
    html += `<h2>Changed pages (${changed.length})</h2>`;
    for (const r of changed) {
      const file = pageToFile(r.page);
      const badge = r.diffPercent > 10 ? "high" : r.diffPercent > 2 ? "medium" : "low";
      html += `<details class="page">
<summary>
<span>${r.page}</span>
<span class="badge ${badge}">${r.diffPercent}% diff</span>
</summary>
<div class="page-body">
<h3>Side by side</h3>
<div class="compare">
<div><h4>Old</h4><img src="screenshots-old/${file}" loading="lazy"></div>
<div><h4>New</h4><img src="screenshots-new/${file}" loading="lazy"></div>
</div>
<h3>Diff overlay</h3>
<div class="diff-img"><img src="screenshots-diff/${file}" loading="lazy"></div>
</div>
</details>`;
    }
  }

  if (identical.length) {
    html += `<h2>Identical pages (${identical.length})</h2><details><summary>Show all</summary><ul>`;
    for (const r of identical) html += `<li>${r.page}</li>`;
    html += `</ul></details>`;
  }

  html += `</body></html>`;

  writeFileSync(join(outputDir, "index.html"), html);
  console.log(`\nHTML report: ${join(outputDir, "index.html")}`);
}

async function main() {
  const { oldSite, newSite, outputDir } = parseArgs();

  if (!existsSync(oldSite)) { console.error(`Not found: ${oldSite}`); process.exit(1); }
  if (!existsSync(newSite)) { console.error(`Not found: ${newSite}`); process.exit(1); }

  const screenshotsOld = join(outputDir, "screenshots-old");
  const screenshotsNew = join(outputDir, "screenshots-new");
  const screenshotsDiff = join(outputDir, "screenshots-diff");
  for (const d of [screenshotsOld, screenshotsNew, screenshotsDiff]) {
    rmSync(d, { recursive: true, force: true });
  }
  mkdirSync(outputDir, { recursive: true });

  const oldPages = collectPages(oldSite);
  const newPages = collectPages(newSite);
  console.log(`Old site: ${oldPages.length} pages`);
  console.log(`New site: ${newPages.length} pages`);

  const onlyInOld = oldPages.filter((p) => !newPages.includes(p));
  const onlyInNew = newPages.filter((p) => !oldPages.includes(p));
  if (onlyInOld.length) console.log(`\nPages only in old:\n  ${onlyInOld.join("\n  ")}`);
  if (onlyInNew.length) console.log(`\nPages only in new:\n  ${onlyInNew.join("\n  ")}`);

  console.log("\nStarting servers...");
  const serverOld = await startServer(oldSite, 8881);
  const serverNew = await startServer(newSite, 8882);

  const browser = await chromium.launch();

  console.log(`\nScreenshotting old site (${oldPages.length} pages)...`);
  const failedOld = await screenshotPages(browser, "http://127.0.0.1:8881", oldPages, screenshotsOld);

  console.log(`Screenshotting new site (${newPages.length} pages)...`);
  const failedNew = await screenshotPages(browser, "http://127.0.0.1:8882", newPages, screenshotsNew);

  await browser.close();
  serverOld.close();
  serverNew.close();

  console.log("\nComparing screenshots...");
  const results = diffScreenshots(screenshotsOld, screenshotsNew, screenshotsDiff);
  const allFailed = new Set([...failedOld, ...failedNew]);
  for (const route of allFailed) {
    results.push({ page: route, status: "failed", diffPixels: 0, diffPercent: 0 });
  }
  generateReport(results, outputDir);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
