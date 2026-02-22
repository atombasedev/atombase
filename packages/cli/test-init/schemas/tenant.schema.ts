import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("tenant", {
  todos: defineTable({
    completed: c.integer().notNull().default(0),
    created_at: c.text().notNull().default("test"),
    id: c.integer().primaryKey(),
    name: c.text().notNull(),

  }),
});
