// Renders the console's education diagrams to standalone SVG files for the
// documentation, so the docs and the UI cannot drift apart: both come from
// web/src/components/EducationDiagrams.tsx.
//
//   cd web && npm run diagrams
//
// The output is committed, like the UI bundle, so building the docs needs no
// Node. Re-run this after changing a diagram component.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const webDir = resolve(here, "../web");
const outDir = resolve(here, "../docs/assets/diagrams");
const entry = resolve(webDir, "src/__diagrams-entry.tsx");
const bundle = resolve(webDir, "node_modules/.cache/diagrams.cjs");

const FIGURES = [
  ["smpp-bind-modes", "SMPPSessionDiagram"],
  ["mt-route-priority", "RoutePriorityDiagram"],
  ["sms-segmentation", "SegmentationChart"],
  ["charging-split", "ChargingSplitChart"],
  ["dlr-levels", "DLRLevelsDiagram"],
];

writeFileSync(
  entry,
  `import { renderToStaticMarkup } from "react-dom/server";
import * as D from "./components/EducationDiagrams";
const figures = ${JSON.stringify(FIGURES)};
const out = {};
for (const [name, component] of figures) {
  const Component = D[component];
  out[name] = renderToStaticMarkup(<Component />);
}
process.stdout.write(JSON.stringify(out));
`,
);

try {
  execFileSync(
    resolve(webDir, "node_modules/.bin/esbuild"),
    [entry, "--bundle", "--format=cjs", "--platform=node", "--jsx=automatic", `--outfile=${bundle}`, "--log-level=error"],
    { cwd: webDir },
  );
  const rendered = JSON.parse(execFileSync(process.execPath, [bundle], { encoding: "utf8" }));
  mkdirSync(outDir, { recursive: true });

  for (const [name] of FIGURES) {
    // The figure component emits only the <svg>; the caption is React and stays
    // in the app. Docs carry the caption as prose beneath the image.
    const svg = rendered[name].match(/<svg[\s\S]*?<\/svg>/)[0];
    const [, width, height] = svg.match(/viewBox="0 0 (\d+) (\d+)"/);
    const standalone = svg
      // The app sizes the figure with CSS; a standalone file should be governed
      // by its own width/height/viewBox instead.
      .replace(/ style="[^"]*"/, "")
      .replace(
        "<svg ",
        `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" ` +
          `font-family="system-ui, -apple-system, Segoe UI, sans-serif" `,
      )
      // A white plate, so the figure stays readable on a dark documentation theme.
      .replace(">", `><rect width="${width}" height="${height}" fill="#ffffff"/>`);
    writeFileSync(resolve(outDir, `${name}.svg`), `${standalone}\n`);
    console.log(`wrote docs/assets/diagrams/${name}.svg`);
  }
} finally {
  rmSync(entry, { force: true });
  rmSync(bundle, { force: true });
}
