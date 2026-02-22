import { defineSchema, defineTable, c } from "./src/index.js";

// Test: Define a schema matching the design doc example
const schema = defineSchema("user-app", {
  users: defineTable({
    id: c.integer().primaryKey(),
    email: c.text().notNull().unique(),
    name: c.text().notNull(),
    avatar_url: c.text(),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }),

  projects: defineTable({
    id: c.integer().primaryKey(),
    user_id: c.integer().notNull().references("users.id"),
    title: c.text().notNull(),
    description: c.text(),
    archived: c.integer().notNull().default(0),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  })
    .fts(["title", "description"])
    .index("idx_user", ["user_id"]),

  tasks: defineTable({
    id: c.integer().primaryKey(),
    project_id: c
      .integer()
      .notNull()
      .references("projects.id", { onDelete: "CASCADE" }),
    title: c.text().notNull(),
    completed: c.integer().notNull().default(0),
    due_date: c.text(),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }).fts(["title"]),
});

// Verify structure
console.log("Schema name:", schema.name);
console.log(
  "Tables:",
  schema.tables.map((t) => t.name)
);
console.log("");

// Check users table
const users = schema.tables.find((t) => t.name === "users")!;
console.log("users pk:", users.pk);
console.log("users columns:", Object.keys(users.columns));
console.log("users.email:", users.columns["email"]);
console.log("");

// Check projects table (has FTS and index)
const projects = schema.tables.find((t) => t.name === "projects")!;
console.log("projects pk:", projects.pk);
console.log("projects.ftsColumns:", projects.ftsColumns);
console.log("projects.indexes:", projects.indexes);
console.log("projects.user_id references:", projects.columns["user_id"]?.references);
console.log("");

// Check tasks table (has CASCADE)
const tasks = schema.tables.find((t) => t.name === "tasks")!;
const projectIdCol = tasks.columns["project_id"];
console.log("tasks.project_id references:", projectIdCol?.references);
console.log("tasks.project_id onDelete:", projectIdCol?.onDelete);

// Output full JSON
console.log("\n--- Full Schema JSON ---");
console.log(JSON.stringify(schema, null, 2));
