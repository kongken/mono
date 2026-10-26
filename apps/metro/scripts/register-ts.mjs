// Lets `node --test` load the workspace's TypeScript sources: the generated API contracts (packages/api-client)
// import each other without a file extension, which the bundler resolves and plain Node does not.
import { register } from "node:module";

register("./ts-resolve.mjs", import.meta.url);
