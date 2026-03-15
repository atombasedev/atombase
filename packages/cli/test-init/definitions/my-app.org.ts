import { defineSchema, defineAccess, defineTable, c, allow, defineOrg, defineMembership, eq, or, sql } from "@atomicbase/definitions";

const schema = defineSchema({
  users: defineTable({
    id: c.integer().primaryKey(),
    email: c.text().notNull().unique(),
    name: c.text().notNull(),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }),
});

export default defineOrg({
  membership: defineMembership({
    roles: ["owner", "admin", "member"],
    management: (role) => ({
      owner: {
        invite: role.admin,
      },
    }),
  }),
  schema,
  access: defineAccess(schema, {
    users: {
      select: ({ auth, prev }) => or(eq(prev.email, auth.id), eq(prev.id, auth.id)),
      insert: allow(),
      update: allow(),
      delete: allow(),
    },
  }),
});
