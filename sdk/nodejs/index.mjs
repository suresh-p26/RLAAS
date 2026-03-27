// ESM re-export wrapper — delegates to the CJS implementation.
import { createRequire } from "module";
const require = createRequire(import.meta.url);
const { RlaasClient, RlaasError } = require("./lib/client.js");

export { RlaasClient, RlaasError };
