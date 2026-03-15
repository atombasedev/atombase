import { Command } from "commander";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const CONFIG_TEMPLATE = `import { defineConfig } from "@atomicbase/cli";

export default defineConfig({
  url: process.env.ATOMICBASE_URL || "http://localhost:8080",
  apiKey: process.env.ATOMICBASE_API_KEY,
  schemas: "./definitions",
});
`;

const EXAMPLE_SCHEMA = `import { defineGlobal, defineSchema, defineAccess, defineTable, c, allow } from "@atomicbase/definitions";

const schema = defineSchema({
  users: defineTable({
    id: c.integer().primaryKey(),
    email: c.text().notNull().unique(),
    name: c.text().notNull(),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }),
});

export default defineGlobal({
  schema,
  access: defineAccess(schema, {
    users: {
      select: allow(),
      insert: allow(),
      update: allow(),
      delete: allow(),
    },
  }),
});
`;

export const initCommand = new Command("init")
  .description("Initialize Atomicbase in the current directory")
  .action(async () => {
    const cwd = process.cwd();

    // Check if already initialized
    if (existsSync(resolve(cwd, "atomicbase.config.ts"))) {
      console.log("Already initialized (atomicbase.config.ts exists)");
      return;
    }

    // Create config file
    writeFileSync(resolve(cwd, "atomicbase.config.ts"), CONFIG_TEMPLATE);
    console.log("Created atomicbase.config.ts");

    // Create definitions directory
    const definitionsDir = resolve(cwd, "definitions");
    if (!existsSync(definitionsDir)) {
      mkdirSync(definitionsDir, { recursive: true });
      console.log("Created definitions/");

      // Create example schema
      writeFileSync(resolve(definitionsDir, "my-app.global.ts"), EXAMPLE_SCHEMA);
      console.log("Created definitions/my-app.global.ts");
    }

    console.log("\nDone! Next steps:");
    console.log("  1. Set ATOMICBASE_URL and ATOMICBASE_API_KEY environment variables");
    console.log("  2. Edit definitions/my-app.global.ts to define your schema and access rules");
    console.log("  3. Run: atomicbase definitions push");
  });
