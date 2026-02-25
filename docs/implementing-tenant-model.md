# Implementing the Unified Tenant Model

This document describes the implementation pattern for enforcing the unified tenant model's access control at the API layer.

## Overview

Every data request must pass through four layers:

1. **Session + Database Resolution** — Validate session, resolve which database to access, get user's role (if org)
2. **Policy Loading** — Load the definition's access policies (cached per version)
3. **Operation Authorization** — Check if the user's role allows this operation
4. **Query Rewriting (RLS)** — Inject WHERE clauses and validate values based on row-level policies

## Types

```go
type AuthContext struct {
    UserID     string
    Role       *string  // nil for global/user databases
    DatabaseID string
}

type Operation string

const (
    OpSelect Operation = "select"
    OpInsert Operation = "insert"
    OpUpdate Operation = "update"
    OpDelete Operation = "delete"
)

type PolicyCondition struct {
    // Parsed AST from r.where(({ auth, old, new }) => ...)
    // e.g., eq(old.author_id, auth.id) becomes:
    Field string  // "old.author_id", "new.author_id", "auth.role"
    Op    string  // "eq", "ne", "gt", "in", etc.
    Value string  // "auth.id", "auth.role", or literal
}

type TablePolicy struct {
    Select *OperationPolicy
    Insert *OperationPolicy
    Update *OperationPolicy
    Delete *OperationPolicy
}

type OperationPolicy struct {
    Allow      bool               // r.allow()
    Conditions []PolicyCondition  // r.where(...)
}

type DefinitionPolicy struct {
    Roles      []string                // ["owner", "admin", "member", "viewer"]
    Management map[string]interface{}  // parsed management permissions
    Access     map[string]*TablePolicy // table name -> policy
}

type Database struct {
    ID           string
    DefinitionID int
    Version      int
    Token        string  // Turso connection token
}
```

## Layer 1: Session + Database Resolution

Validate the session and resolve database access. No caching needed — SQLite indexed lookups are fast (~50μs each).

```go
func ResolveRequest(db *sql.DB, req *http.Request) (*AuthContext, *Database, error) {
    // Extract session token
    token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
    if token == "" {
        return nil, nil, ErrUnauthenticated
    }

    // Validate session and get user (single indexed query)
    var userID string
    var userDatabaseID *string
    err := db.QueryRow(`
        SELECT s.user_id, u.database_id
        FROM atomicbase_sessions s
        JOIN atomicbase_users u ON u.id = s.user_id
        WHERE s.id = ? AND s.expires_at > datetime('now')
    `, token).Scan(&userID, &userDatabaseID)
    if err != nil {
        return nil, nil, ErrInvalidSession
    }

    // Resolve database based on request type
    dbType := req.Header.Get("X-Database-Type")  // "global", "user", "org"

    switch dbType {
    case "global":
        return resolveGlobalAccess(db, req, userID)
    case "user":
        return resolveUserAccess(db, userID, userDatabaseID)
    case "org":
        return resolveOrgAccess(db, req, userID)
    default:
        return nil, nil, ErrInvalidDatabaseType
    }
}

func resolveGlobalAccess(db *sql.DB, req *http.Request, userID string) (*AuthContext, *Database, error) {
    defName := req.Header.Get("X-Database")

    var database Database
    err := db.QueryRow(`
        SELECT d.id, d.definition_id, d.definition_version, d.token
        FROM atomicbase_databases d
        JOIN atomicbase_definitions def ON def.id = d.definition_id
        WHERE def.name = ? AND def.definition_type = 'global'
    `, defName).Scan(&database.ID, &database.DefinitionID, &database.Version, &database.Token)
    if err != nil {
        return nil, nil, ErrDatabaseNotFound
    }

    return &AuthContext{
        UserID:     userID,
        Role:       nil,  // no role for global
        DatabaseID: database.ID,
    }, &database, nil
}

func resolveUserAccess(db *sql.DB, userID string, userDatabaseID *string) (*AuthContext, *Database, error) {
    if userDatabaseID == nil {
        return nil, nil, ErrNoUserDatabase
    }

    var database Database
    err := db.QueryRow(`
        SELECT id, definition_id, definition_version, token
        FROM atomicbase_databases
        WHERE id = ?
    `, *userDatabaseID).Scan(&database.ID, &database.DefinitionID, &database.Version, &database.Token)
    if err != nil {
        return nil, nil, ErrDatabaseNotFound
    }

    return &AuthContext{
        UserID:     userID,
        Role:       nil,  // no role for user databases
        DatabaseID: database.ID,
    }, &database, nil
}

func resolveOrgAccess(db *sql.DB, req *http.Request, userID string) (*AuthContext, *Database, error) {
    orgID := req.Header.Get("X-Organization")

    var database Database
    var role string
    err := db.QueryRow(`
        SELECT d.id, d.definition_id, d.definition_version, d.token, m.role
        FROM atomicbase_organizations o
        JOIN atomicbase_databases d ON d.id = o.database_id
        JOIN atomicbase_membership m ON m.organization_id = o.id
        WHERE o.id = ? AND m.user_id = ?
    `, orgID, userID).Scan(&database.ID, &database.DefinitionID, &database.Version, &database.Token, &role)
    if err != nil {
        return nil, nil, ErrNotMember
    }

    return &AuthContext{
        UserID:     userID,
        Role:       &role,
        DatabaseID: database.ID,
    }, &database, nil
}
```

### Auth Context by Database Type

| Type   | `auth.id`         | `auth.role`        |
|--------|-------------------|--------------------|
| Global | ✓ (user ID)       | —                  |
| User   | ✓ (user ID/owner) | —                  |
| Org    | ✓ (user ID)       | ✓ (from membership)|

## Layer 2: Policy Loading

Load and cache policies per definition version. Cache is invalidated naturally when version changes.

```go
var policyCache = sync.Map{}  // map[string]*DefinitionPolicy

func LoadPolicy(db *sql.DB, definitionID int, version int) (*DefinitionPolicy, error) {
    cacheKey := fmt.Sprintf("%d_%d", definitionID, version)

    // Check cache first
    if cached, ok := policyCache.Load(cacheKey); ok {
        return cached.(*DefinitionPolicy), nil
    }

    // Load from database
    var schemaJSON, accessJSON string
    var rolesJSON, managementJSON sql.NullString
    err := db.QueryRow(`
        SELECT h.schema_json, h.access_json, d.roles_json, d.management_json
        FROM atomicbase_definitions_history h
        JOIN atomicbase_definitions d ON d.id = h.definition_id
        WHERE h.definition_id = ? AND h.version = ?
    `, definitionID, version).Scan(&schemaJSON, &accessJSON, &rolesJSON, &managementJSON)
    if err != nil {
        return nil, err
    }

    // Parse JSON into policy structs
    policy := &DefinitionPolicy{
        Access: parseAccessPolicy(accessJSON),
    }
    if rolesJSON.Valid {
        policy.Roles = parseRoles(rolesJSON.String)
    }
    if managementJSON.Valid {
        policy.Management = parseManagement(managementJSON.String)
    }

    // Cache and return
    policyCache.Store(cacheKey, policy)
    return policy, nil
}

func parseAccessPolicy(accessJSON string) map[string]*TablePolicy {
    // Parse JSON like:
    // {
    //   "posts": {
    //     "select": { "allow": true },
    //     "insert": { "conditions": [{"field": "new.author_id", "op": "eq", "value": "auth.id"}] },
    //     "update": { "conditions": [{"field": "old.author_id", "op": "eq", "value": "auth.id"}] },
    //     "delete": { "conditions": [{"field": "old.author_id", "op": "eq", "value": "auth.id"}] }
    //   }
    // }

    var raw map[string]map[string]json.RawMessage
    json.Unmarshal([]byte(accessJSON), &raw)

    policies := make(map[string]*TablePolicy)
    for table, ops := range raw {
        tp := &TablePolicy{}
        for op, data := range ops {
            var opPolicy OperationPolicy
            json.Unmarshal(data, &opPolicy)

            switch op {
            case "select":
                tp.Select = &opPolicy
            case "insert":
                tp.Insert = &opPolicy
            case "update":
                tp.Update = &opPolicy
            case "delete":
                tp.Delete = &opPolicy
            }
        }
        policies[table] = tp
    }
    return policies
}
```

## Layer 3: Operation Authorization

Check if the operation is allowed based on role and policy.

```go
func AuthorizeOperation(auth *AuthContext, policy *DefinitionPolicy, table string, op Operation) (*OperationPolicy, error) {
    // Get table policy
    tablePolicy, ok := policy.Access[table]
    if !ok {
        return nil, ErrTableNotFound
    }

    // Get operation policy
    var opPolicy *OperationPolicy
    switch op {
    case OpSelect:
        opPolicy = tablePolicy.Select
    case OpInsert:
        opPolicy = tablePolicy.Insert
    case OpUpdate:
        opPolicy = tablePolicy.Update
    case OpDelete:
        opPolicy = tablePolicy.Delete
    }

    if opPolicy == nil {
        return nil, ErrOperationNotAllowed
    }

    // Check role-based conditions
    for _, cond := range opPolicy.Conditions {
        if cond.Field == "auth.role" {
            if auth.Role == nil {
                return nil, ErrOperationNotAllowed
            }
            if !matchesRoleCondition(cond, *auth.Role) {
                return nil, ErrOperationNotAllowed
            }
        }
    }

    return opPolicy, nil
}

func matchesRoleCondition(cond PolicyCondition, role string) bool {
    switch cond.Op {
    case "eq":
        return role == cond.Value
    case "ne":
        return role != cond.Value
    case "in":
        roles := strings.Split(cond.Value, ",")
        for _, r := range roles {
            if strings.TrimSpace(r) == role {
                return true
            }
        }
        return false
    }
    return false
}
```

## Layer 4: Query Rewriting (Row-Level Security)

Inject WHERE clauses for `old` conditions, validate values for `new` conditions.

### Context Availability by Operation

| Operation | `old` | `new` |
|-----------|-------|-------|
| SELECT    | ✓     | —     |
| INSERT    | —     | ✓     |
| UPDATE    | ✓     | ✓     |
| DELETE    | ✓     | —     |

- **`old`** — the existing row being acted upon
- **`new`** — the resulting row after modification

### Query Rewriter

```go
type QueryRewriter struct {
    Auth   *AuthContext
    Policy *OperationPolicy
    Table  string
}

// RewriteSelect injects WHERE clause for "old" conditions
// Used for SELECT and DELETE
func (qr *QueryRewriter) RewriteSelect(originalWhere string) string {
    if qr.Policy.Allow {
        return originalWhere
    }

    rlsCondition := qr.buildOldCondition()
    if rlsCondition == "" {
        return originalWhere
    }
    if originalWhere == "" {
        return rlsCondition
    }
    return fmt.Sprintf("(%s) AND (%s)", originalWhere, rlsCondition)
}

// ValidateInsert checks "new" values before execution
// Used for INSERT and UPDATE
func (qr *QueryRewriter) ValidateInsert(values map[string]any) error {
    if qr.Policy.Allow {
        return nil
    }

    for _, cond := range qr.Policy.Conditions {
        if !strings.HasPrefix(cond.Field, "new.") {
            continue
        }

        col := strings.TrimPrefix(cond.Field, "new.")
        newValue, exists := values[col]
        if !exists {
            continue  // column not being set
        }

        expected := qr.resolveValue(cond.Value)
        if !matchCondition(cond.Op, newValue, expected) {
            return &PolicyViolationError{
                Field:    cond.Field,
                Expected: expected,
                Actual:   newValue,
            }
        }
    }
    return nil
}

// RewriteUpdate handles both "old" (WHERE) and "new" (validation)
func (qr *QueryRewriter) RewriteUpdate(originalWhere string, newValues map[string]any) (string, error) {
    // Validate new values first
    if err := qr.ValidateInsert(newValues); err != nil {
        return "", err
    }

    // Rewrite WHERE for old conditions
    return qr.RewriteSelect(originalWhere), nil
}

// RewriteDelete is the same as RewriteSelect (only "old" conditions)
func (qr *QueryRewriter) RewriteDelete(originalWhere string) string {
    return qr.RewriteSelect(originalWhere)
}

// buildOldCondition generates SQL WHERE clause from "old.*" conditions
func (qr *QueryRewriter) buildOldCondition() string {
    var conditions []string

    for _, cond := range qr.Policy.Conditions {
        if !strings.HasPrefix(cond.Field, "old.") {
            continue
        }

        col := strings.TrimPrefix(cond.Field, "old.")
        value := qr.resolveValue(cond.Value)

        switch cond.Op {
        case "eq":
            conditions = append(conditions, fmt.Sprintf("%s = %s",
                sqlIdentifier(col), sqlQuote(value)))
        case "ne":
            conditions = append(conditions, fmt.Sprintf("%s != %s",
                sqlIdentifier(col), sqlQuote(value)))
        case "in":
            conditions = append(conditions, fmt.Sprintf("%s IN (%s)",
                sqlIdentifier(col), sqlQuoteList(value)))
        case "gt":
            conditions = append(conditions, fmt.Sprintf("%s > %s",
                sqlIdentifier(col), sqlQuote(value)))
        case "gte":
            conditions = append(conditions, fmt.Sprintf("%s >= %s",
                sqlIdentifier(col), sqlQuote(value)))
        case "lt":
            conditions = append(conditions, fmt.Sprintf("%s < %s",
                sqlIdentifier(col), sqlQuote(value)))
        case "lte":
            conditions = append(conditions, fmt.Sprintf("%s <= %s",
                sqlIdentifier(col), sqlQuote(value)))
        }
    }

    if len(conditions) == 0 {
        return ""
    }
    return strings.Join(conditions, " AND ")
}

// resolveValue converts policy values to actual values
func (qr *QueryRewriter) resolveValue(value string) any {
    switch value {
    case "auth.id":
        return qr.Auth.UserID
    case "auth.role":
        if qr.Auth.Role != nil {
            return *qr.Auth.Role
        }
        return nil
    default:
        // Check if it's a reference to old.* (for update constraints like new.author_id = old.author_id)
        if strings.HasPrefix(value, "old.") {
            // This needs special handling - return a marker that buildOldCondition understands
            return &ColumnReference{Column: strings.TrimPrefix(value, "old.")}
        }
        return value  // literal value
    }
}

func matchCondition(op string, actual, expected any) bool {
    switch op {
    case "eq":
        return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
    case "ne":
        return fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected)
    case "in":
        list, ok := expected.([]any)
        if !ok {
            return false
        }
        for _, v := range list {
            if fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", v) {
                return true
            }
        }
        return false
    }
    return false
}

// SQL helpers
func sqlIdentifier(name string) string {
    return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func sqlQuote(value any) string {
    if value == nil {
        return "NULL"
    }
    switch v := value.(type) {
    case string:
        return "'" + strings.ReplaceAll(v, "'", "''") + "'"
    case int, int64, float64:
        return fmt.Sprintf("%v", v)
    case bool:
        if v {
            return "1"
        }
        return "0"
    case *ColumnReference:
        return sqlIdentifier(v.Column)
    default:
        return "'" + strings.ReplaceAll(fmt.Sprintf("%v", v), "'", "''") + "'"
    }
}

type ColumnReference struct {
    Column string
}
```

## Full Request Handler

Putting it all together:

```go
func HandleDataRequest(platformDB *sql.DB, req *http.Request) (*Response, error) {
    // Layer 1: Resolve session and database access
    auth, database, err := ResolveRequest(platformDB, req)
    if err != nil {
        return nil, err
    }

    // Layer 2: Load policies for this definition version
    policy, err := LoadPolicy(platformDB, database.DefinitionID, database.Version)
    if err != nil {
        return nil, err
    }

    // Parse the incoming query
    query, err := ParseQuery(req)
    if err != nil {
        return nil, err
    }

    // Layer 3: Authorize the operation
    opPolicy, err := AuthorizeOperation(auth, policy, query.Table, query.Operation)
    if err != nil {
        return nil, err
    }

    // Layer 4: Rewrite query with RLS
    rewriter := &QueryRewriter{Auth: auth, Policy: opPolicy, Table: query.Table}

    var finalSQL string
    var args []any

    switch query.Operation {
    case OpSelect:
        where := rewriter.RewriteSelect(query.Where)
        finalSQL = fmt.Sprintf("SELECT %s FROM %s", query.Columns, sqlIdentifier(query.Table))
        if where != "" {
            finalSQL += " WHERE " + where
        }
        if query.OrderBy != "" {
            finalSQL += " ORDER BY " + query.OrderBy
        }
        if query.Limit > 0 {
            finalSQL += fmt.Sprintf(" LIMIT %d", query.Limit)
        }

    case OpInsert:
        if err := rewriter.ValidateInsert(query.Values); err != nil {
            return nil, err
        }
        cols, vals, placeholders := buildInsertParts(query.Values)
        finalSQL = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
            sqlIdentifier(query.Table), cols, placeholders)
        args = vals

    case OpUpdate:
        where, err := rewriter.RewriteUpdate(query.Where, query.SetValues)
        if err != nil {
            return nil, err
        }
        setClause, vals := buildSetClause(query.SetValues)
        finalSQL = fmt.Sprintf("UPDATE %s SET %s", sqlIdentifier(query.Table), setClause)
        if where != "" {
            finalSQL += " WHERE " + where
        }
        args = vals

    case OpDelete:
        where := rewriter.RewriteDelete(query.Where)
        finalSQL = fmt.Sprintf("DELETE FROM %s", sqlIdentifier(query.Table))
        if where != "" {
            finalSQL += " WHERE " + where
        }
    }

    // Execute against tenant database
    tenantDB, err := connectTenant(database.Token)
    if err != nil {
        return nil, err
    }
    defer tenantDB.Close()

    result, err := tenantDB.ExecContext(req.Context(), finalSQL, args...)
    if err != nil {
        return nil, err
    }

    return &Response{
        RowsAffected: result.RowsAffected(),
    }, nil
}
```

## Example Flows

### Example 1: Global Database Select

```
Request:
  GET /data/posts
  Authorization: Bearer session-123
  X-Database-Type: global
  X-Database: marketplace

Policy:
  select: r.allow()

Flow:
  1. Session lookup → user-456
  2. Global database lookup → marketplace DB
  3. Policy: select is r.allow()
  4. No rewriting needed
  5. Execute: SELECT * FROM posts
```

### Example 2: User Database Insert

```
Request:
  POST /data/notes
  Authorization: Bearer session-123
  X-Database-Type: user
  Body: { "content": "Hello", "user_id": "user-456" }

Policy:
  insert: r.where(({ auth, new }) => eq(new.user_id, auth.id))

Flow:
  1. Session lookup → user-456, database_id: db-789
  2. User database lookup → db-789
  3. Policy: insert has new.user_id = auth.id condition
  4. Validate: new.user_id ("user-456") == auth.id ("user-456") ✓
  5. Execute: INSERT INTO notes (content, user_id) VALUES ('Hello', 'user-456')
```

### Example 3: Org Database Delete

```
Request:
  DELETE /data/posts?id=123
  Authorization: Bearer session-123
  X-Database-Type: org
  X-Organization: acme-corp

Policy:
  delete: r.where(({ auth, old }) => eq(old.author_id, auth.id))

Flow:
  1. Session lookup → user-456
  2. Org membership lookup → role: "member"
  3. Policy: delete has old.author_id = auth.id condition
  4. Rewrite WHERE: id = 123 AND author_id = 'user-456'
  5. Execute: DELETE FROM posts WHERE id = 123 AND author_id = 'user-456'
  6. If row exists but author_id != user-456 → 0 rows affected (silent denial)
```

### Example 4: Org Database Update with old/new

```
Request:
  PATCH /data/posts?id=123
  Authorization: Bearer session-123
  X-Database-Type: org
  X-Organization: acme-corp
  Body: { "title": "New Title", "author_id": "user-789" }

Policy:
  update: r.where(({ auth, old, new }) =>
    eq(old.author_id, auth.id) && eq(new.author_id, old.author_id)
  )

Flow:
  1. Session lookup → user-456
  2. Org membership lookup → role: "member"
  3. Policy: update has both old and new conditions
  4. Validate new: new.author_id ("user-789") == old.author_id → DEFER (need old value)
     - Alternative: Validate new.author_id == auth.id (if policy simplified)
     - Or: Reject because new.author_id != auth.id
  5. Rewrite WHERE: id = 123 AND author_id = 'user-456'
  6. For new.author_id = old.author_id constraint:
     - Either reject upfront if input author_id != auth.id
     - Or use: UPDATE ... SET ... WHERE ... AND 'user-789' = author_id
```

## Error Handling

```go
var (
    ErrUnauthenticated     = &APIError{Code: 401, Message: "authentication required"}
    ErrInvalidSession      = &APIError{Code: 401, Message: "invalid or expired session"}
    ErrNotMember           = &APIError{Code: 403, Message: "not a member of this organization"}
    ErrNoUserDatabase      = &APIError{Code: 404, Message: "user has no database"}
    ErrDatabaseNotFound    = &APIError{Code: 404, Message: "database not found"}
    ErrTableNotFound       = &APIError{Code: 404, Message: "table not found"}
    ErrOperationNotAllowed = &APIError{Code: 403, Message: "operation not allowed"}
    ErrInvalidDatabaseType = &APIError{Code: 400, Message: "invalid database type"}
)

type PolicyViolationError struct {
    Field    string
    Expected any
    Actual   any
}

func (e *PolicyViolationError) Error() string {
    return fmt.Sprintf("policy violation: %s expected %v, got %v", e.Field, e.Expected, e.Actual)
}

func (e *PolicyViolationError) Code() int {
    return 403
}
```

## Performance Considerations

1. **No caching needed for session/membership lookups** — SQLite indexed queries are ~50μs
2. **Cache policies per definition version** — Loaded once, cached until version changes
3. **Single query execution** — Query rewriting avoids multiple roundtrips
4. **Fail fast** — Validate `new` values before execution when possible
5. **Use parameterized queries** — Prevent SQL injection, allow query plan caching
