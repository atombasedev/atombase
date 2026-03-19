import {
  c,
  defineAccess,
  defineMembership,
  defineOrg,
  defineProvision,
  defineSchema,
  defineTable,
  eq,
  sql,
} from "@atomicbase/definitions";

const schema = defineSchema({
  todos: defineTable({
    id: c.text().primaryKey(),
    title: c.text().notNull(),
    completed: c.integer().notNull().default(0),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
    updated_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }),
});

export default defineOrg({
  name: "todo-team",
  provision: defineProvision(({ auth }) => eq(auth.status, "authenticated")),
  membership: defineMembership({
    roles: ["owner", "member"],
    management: (role) => ({
      owner: {
        invite: role.any(),
        assignRole: role.any(),
        removeMember: role.any(),
        updateOrg: true,
        deleteOrg: true,
        transferOwnership: true,
      },
    }),
  }),
  schema,
  access: defineAccess(schema, {
    todos: {
      select: ({ auth }) => eq(auth.status, "member"),
      insert: ({ auth }) => eq(auth.status, "member"),
      update: ({ auth }) => eq(auth.status, "member"),
      delete: ({ auth }) => eq(auth.status, "member"),
    },
  }),
});
