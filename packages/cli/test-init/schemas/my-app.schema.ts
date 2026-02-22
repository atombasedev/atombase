import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("my-app", {
  users: defineTable({
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
    email: c.text().notNull().unique(),
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
  }),
});
