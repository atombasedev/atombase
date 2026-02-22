import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("todos", {
  todos: defineTable({
    completed: c.integer().notNull().default(0),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
    id: c.integer().primaryKey(),
    title: c.text().notNull(),
    updated_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }),
});
