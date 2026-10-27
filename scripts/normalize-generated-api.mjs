// protoc-gen-es emits an extra blank line at EOF. Keep generated TypeScript at
// one final newline so newly added contracts pass git diff --check.
import { readdirSync, readFileSync, writeFileSync } from "node:fs";

function normalize(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = new URL(entry.name, directory);
    if (entry.isDirectory()) {
      normalize(new URL(`${entry.name}/`, directory));
    } else if (entry.isFile() && entry.name.endsWith("_pb.ts")) {
      const source = readFileSync(file, "utf8");
      const normalized = source.replace(/\n+$/, "\n");
      if (source !== normalized) writeFileSync(file, normalized);
    }
  }
}

normalize(new URL("../packages/api-client/src/", import.meta.url));
