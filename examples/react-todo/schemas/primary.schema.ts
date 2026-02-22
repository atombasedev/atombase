import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("primary", {
  users: defineTable({
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
    email: c.text().notNull(),
    google_id: c.text().notNull().unique(),
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    picture: c.text(),
    tenant_name: c.text().notNull().unique(),
  }).index("idx_google_id", ["google_id"]),

  sessions: defineTable({
    created_at: c.integer().notNull(),
    expires_at: c.integer().notNull(),
    id: c.text().primaryKey(),
    user_id: c.integer().notNull().references("users.id", { onDelete: "CASCADE" }),
  }).index("idx_user_id", ["user_id"]),
});
