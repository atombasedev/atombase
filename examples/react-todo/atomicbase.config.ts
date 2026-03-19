import { defineConfig } from "@atomicbase/cli";

export default defineConfig({
  url: process.env.NEXT_PUBLIC_ATOMICBASE_URL || "http://localhost:8080",
  apiKey: process.env.ATOMICBASE_API_KEY,
  definitions: "./definitions",
});
