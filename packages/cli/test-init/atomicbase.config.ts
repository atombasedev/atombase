import { defineConfig } from "@atomicbase/cli";

export default defineConfig({
  url: process.env.ATOMICBASE_URL || "https://test.atomhost.dev",
  apiKey: process.env.ATOMICBASE_API_KEY || "rM6iLhjUC+NfzmeGYNmU5C6zNrqz86zPFzFREqafdb0=",
  schemas: "./schemas",
});
