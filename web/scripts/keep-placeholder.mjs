// vite's emptyOutDir removes the tracked .gitkeep that lets `go build` embed
// the directory before any UI build exists. Put it back after every build.
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

writeFileSync(fileURLToPath(new URL("../../runtime/internal/webui/static/.gitkeep", import.meta.url)), "");
