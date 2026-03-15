import { defineGlobal, defineSchema, defineAccess, definePolicy, defineTable, c, r, sql } from "@atomicbase/definitions";

export default defineGlobal({
  schema: defineSchema({
    todos: defineTable({
      completed: c.integer().notNull().default(0),
      created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
      id: c.integer().primaryKey(),
      title: c.text()
    }),
  }),
  access: defineAccess({
    todos: definePolicy({
      select: r.allow(),
      insert: r.allow(),
      update: r.allow(),
      delete: r.allow(),
    }),
  }),
});
