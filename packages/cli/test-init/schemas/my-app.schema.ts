import { defineGlobal, defineSchema, defineAccess, definePolicy, defineTable, c, r, sql } from "@atomicbase/definitions";

export default defineGlobal({
  schema: defineSchema({
    users: defineTable({
      created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
      email: c.text().notNull().unique(),
      id: c.integer().primaryKey(),
      name: c.text().notNull(),
    }),
  }),
  access: defineAccess({
    users: definePolicy({
      select: r.allow(),
      insert: r.allow(),
      update: r.allow(),
      delete: r.allow(),
    }),
  }),
});
