# Implementing the Unified Tenant Model

> [!CAUTION]
> Historical implementation notes. This document contains older naming and example APIs in places. Use `docs/definition-model.md` and the current code as the source of truth for the active SDK and definition surface.

This document describes the implementation pattern for enforcing the unified tenant model's access control at the API layer.

## Overview

Every data request must pass through four layers:

1. **Session + Database Resolution** — Validate session, resolve which database to access, get user's role (if org)
2. **Policy Loading** — Load access policies from normalized table (cached per definition:version:table:operation)
3. **Operation Authorization** — Check if policy exists for the operation
4. **Query Rewriting (RLS)** — Inject WHERE clauses and validate values based on row-level policies

## Types

```go
type AuthContext struct {
    UserID       string
    Role         *string  // nil for global/user databases
    DatabaseID   string
    DefinitionID int
    Version      int
}

type Operation string

const (
    OpSelect Operation = "select"
    OpInsert Operation = "insert"
    OpUpdate Operation = "update"
    OpDelete Operation = "delete"
)

// Condition represents a policy condition with support for AND/OR/NOT nesting.
// Either a leaf condition (Field/Op/Value) or a boolean combinator (And/Or/Not).
type Condition struct {
    // Leaf condition fields
    Field string `json:"field,omitempty"` // "old.author_id", "new.status", "auth.role"
    Op    string `json:"op,omitempty"`    // "eq", "ne", "gt", "gte", "lt", "lte", "in", "is", "is_not"
    Value any    `json:"value,omitempty"` // "auth.id", "new.X", literal, or null

    // Boolean combinators (only one should be set)
    And []Condition `json:"and,omitempty"`
    Or  []Condition `json:"or,omitempty"`
    Not *Condition  `json:"not,omitempty"`
}

func (c *Condition) IsLeaf() bool {
    return c.Field != ""
}

// AccessPolicy represents a single table/operation policy from atombase_access_policies
type AccessPolicy struct {
    Condition *Condition // parsed from conditions_json, nil = allow all
}

// IsAllow returns true if this is an r.allow() policy (no conditions)
func (p *AccessPolicy) IsAllow() bool {
    return p.Condition == nil
}
```

## Session Management

### Auth API Response

Auth endpoints return tokens in response body (not Set-Cookie). This avoids cross-domain cookie issues since atombase and the frontend are typically on different domains.

```go
// POST /auth/signin
type SignInResponse struct {
    SessionID string `json:"sessionId"`
    Secret    string `json:"secret"`
    ExpiresAt string `json:"expiresAt"`
}

// Client combines into token: "sessionId:secret"
// Client stores token (SDK handles via memory or cookies on app's domain)
// Client sends: Authorization: Bearer sessionId:secret
```

### Session Validation

Sessions are the hottest table - read on every request, written to periodically. The validation pattern optimizes for this.

```go
const (
    inactivityTimeout     = 10 * 24 * time.Hour  // 10 days - sessions expire after inactivity
    activityCheckInterval = 1 * time.Hour        // 1 hour - update last_verified_at at most this often
)

func validateSession(db *sql.DB, sessionID, providedSecret string) (*Session, error) {
    var session Session
    err := db.QueryRow(`
        SELECT id, secret_hash, user_id, last_verified_at, expires_at
        FROM atombase_sessions
        WHERE id = ?
    `, sessionID).Scan(&session.ID, &session.SecretHash, &session.UserID,
        &session.LastVerifiedAt, &session.ExpiresAt)
    if err != nil {
        return nil, ErrInvalidSession
    }

    // Constant-time comparison in Go (prevents timing attacks)
    if subtle.ConstantTimeCompare(hashSecret(providedSecret), session.SecretHash) != 1 {
        return nil, ErrInvalidSession
    }

    now := time.Now()

    // Check hard expiry
    if now.After(session.ExpiresAt) {
        return nil, ErrSessionExpired
    }

    // Check inactivity (soft expiry)
    if now.Sub(session.LastVerifiedAt) > inactivityTimeout {
        return nil, ErrSessionInactive
    }

    // Update last_verified_at only if interval has passed (reduces writes)
    if now.Sub(session.LastVerifiedAt) > activityCheckInterval {
        db.Exec(`
            UPDATE atombase_sessions
            SET last_verified_at = datetime('now')
            WHERE id = ?
        `, sessionID)
    }

    return &session, nil
}
```

**Key points:**
- Hash verified in Go with `subtle.ConstantTimeCompare` (prevents timing attacks)
- Two expiry mechanisms: hard (`expires_at`) and soft (inactivity based on `last_verified_at`)
- Writes only every ~1 hour per active session, not per request
- Expired session cleanup via background job (not shown)

## Layer 1: Session + Database Resolution

Parse the `Database` header and run the appropriate query. Each query validates session and resolves database in one indexed lookup.

```go
func ResolveRequest(db *sql.DB, req *http.Request) (*AuthContext, error) {
    // Extract session token (format: "sessionID:secret")
    token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
    if token == "" {
        return nil, ErrUnauthenticated
    }

    // Split session ID and secret
    parts := strings.SplitN(token, ":", 2)
    if len(parts) != 2 {
        return nil, ErrInvalidToken
    }
    sessionID, secret := parts[0], parts[1]

    // Validate session first
    session, err := validateSession(db, sessionID, secret)
    if err != nil {
        return nil, err
    }

    // Parse Database header: "type:name"
    dbHeader := req.Header.Get("Database")
    dbParts := strings.SplitN(dbHeader, ":", 2)
    if len(dbParts) != 2 {
        return nil, ErrInvalidDatabaseHeader
    }
    dbType, dbName := dbParts[0], dbParts[1]

    switch dbType {
    case "global":
        return resolveGlobal(db, session.UserID, dbName)
    case "user":
        return resolveUser(db, session.UserID, dbName)
    case "org":
        return resolveOrg(db, session.UserID, dbName)
    default:
        return nil, ErrInvalidDatabaseType
    }
}
```

### Global (`Database: global:<definition_name>`)

```go
func resolveGlobal(db *sql.DB, userID, defName string) (*AuthContext, error) {
    var auth AuthContext
    auth.UserID = userID
    err := db.QueryRow(`
        SELECT d.id, d.definition_id, d.definition_version
        FROM atombase_definitions def
        JOIN atombase_databases d ON d.definition_id = def.id
        WHERE def.name = ? AND def.definition_type = 'global'
    `, defName).Scan(&auth.DatabaseID, &auth.DefinitionID, &auth.Version)
    if err != nil {
        return nil, ErrDatabaseNotFound
    }
    return &auth, nil
}
```

### User (`Database: user:<definition_name>`)

```go
func resolveUser(db *sql.DB, userID, defName string) (*AuthContext, error) {
    var auth AuthContext
    auth.UserID = userID
    err := db.QueryRow(`
        SELECT d.id, d.definition_id, d.definition_version
        FROM atombase_users u
        JOIN atombase_databases d ON d.id = u.database_id
        JOIN atombase_definitions def ON def.id = d.definition_id
        WHERE u.id = ? AND def.name = ? AND def.definition_type = 'user'
    `, userID, defName).Scan(&auth.DatabaseID, &auth.DefinitionID, &auth.Version)
    if err != nil {
        return nil, ErrNoUserDatabase
    }
    return &auth, nil
}
```

### Org (`Database: org:<organization_id>`)

```go
func resolveOrg(db *sql.DB, userID, orgID string) (*AuthContext, error) {
    var auth AuthContext
    auth.UserID = userID
    var role string
    err := db.QueryRow(`
        SELECT m.role, d.id, d.definition_id, d.definition_version
        FROM atombase_membership m
        JOIN atombase_organizations o ON o.id = m.organization_id
        JOIN atombase_databases d ON d.id = o.database_id
        WHERE m.user_id = ? AND m.organization_id = ?
    `, userID, orgID).Scan(&role, &auth.DatabaseID, &auth.DefinitionID, &auth.Version)
    if err != nil {
        return nil, ErrNotMember
    }
    auth.Role = &role
    return &auth, nil
}
```

### Auth Context by Database Type

| Type   | `auth.id`         | `auth.role`        |
|--------|-------------------|--------------------|
| Global | ✓ (user ID)       | —                  |
| User   | ✓ (user ID/owner) | —                  |
| Org    | ✓ (user ID)       | ✓ (from membership)|

## Policy Compilation

Access policy conditions compile entirely to runtime enforcement. This keeps authorization logic in the API layer where it can be bypassed by system operations.

### Condition JSON Format

Conditions support AND/OR/NOT nesting with a recursive structure:

```json
// Simple conditions (implicit AND when in array)
[
  {"field": "old.author_id", "op": "eq", "value": "auth.id"},
  {"field": "old.deleted_at", "op": "is", "value": null}
]

// OR: author can edit OR admin can edit anything
{
  "or": [
    {"field": "old.author_id", "op": "eq", "value": "auth.id"},
    {"field": "auth.role", "op": "eq", "value": "admin"}
  ]
}

// Nested: admin can edit drafts, author can edit their own anything
{
  "or": [
    {"field": "old.author_id", "op": "eq", "value": "auth.id"},
    {
      "and": [
        {"field": "auth.role", "op": "eq", "value": "admin"},
        {"field": "old.status", "op": "eq", "value": "draft"}
      ]
    }
  ]
}

// NOT: can delete if not published
{"not": {"field": "old.status", "op": "eq", "value": "published"}}

// Complex: NOT(published) AND (author OR admin)
{
  "and": [
    {"not": {"field": "old.status", "op": "eq", "value": "published"}},
    {
      "or": [
        {"field": "old.author_id", "op": "eq", "value": "auth.id"},
        {"field": "auth.role", "op": "eq", "value": "admin"}
      ]
    }
  ]
}
```

**Operators:**
- `eq`, `ne`, `gt`, `gte`, `lt`, `lte` — comparison
- `in` — value in list
- `is` — NULL check: `{"field": "old.deleted_at", "op": "is", "value": null}`
- `is_not` — NOT NULL check: `{"field": "old.deleted_at", "op": "is_not", "value": null}`

**Value references:**
- `"auth.id"` — current user's ID
- `"auth.role"` — current user's role (org databases only)
- `"new.X"` — value of column X from request payload
- `"old.X"` — value of column X from existing row
- Literals: `"draft"`, `123`, `true`, `null`

### Compile-Time Validation

When a definition is pushed, validate that conditions use valid context for each operation:

| Operation | `old.*` valid | `new.*` valid | `auth.role` valid |
|-----------|---------------|---------------|-------------------|
| SELECT    | ✓             | ✗             | org only          |
| INSERT    | ✗             | ✓             | org only          |
| UPDATE    | ✓             | ✓             | org only          |
| DELETE    | ✓             | ✗             | org only          |

**Reject at push time if:**
- `old.*` condition in INSERT policy (no existing row)
- `new.*` condition in SELECT/DELETE policy (no new values)
- `auth.role` condition in global/user definition (no roles)
- Referenced column doesn't exist in schema
- Type mismatch (e.g., comparing string column to integer)

```go
func validateConditionContext(cond *Condition, op Operation, defType string) error {
    if cond.IsLeaf() {
        // Check old.* context
        if strings.HasPrefix(cond.Field, "old.") && op == OpInsert {
            return fmt.Errorf("old.* not valid for INSERT: %s", cond.Field)
        }

        // Check new.* context
        if strings.HasPrefix(cond.Field, "new.") && (op == OpSelect || op == OpDelete) {
            return fmt.Errorf("new.* not valid for %s: %s", op, cond.Field)
        }

        // Check auth.role context
        if cond.Field == "auth.role" || cond.Value == "auth.role" {
            if defType != "organization" {
                return fmt.Errorf("auth.role only valid for organization definitions")
            }
        }
        return nil
    }

    // Recurse for AND/OR/NOT
    for _, sub := range cond.And {
        if err := validateConditionContext(&sub, op, defType); err != nil {
            return err
        }
    }
    // ... same for Or, Not
    return nil
}
```

### Compilation Rules

**Conditions referencing `old.*`** → Rearrange algebraically, becomes WHERE clause

| Condition | Rearranged | WHERE Clause |
|-----------|------------|--------------|
| `old.author_id = auth.id` | as-is | `author_id = ?` (auth.id) |
| `old.status = 'draft'` | as-is | `status = 'draft'` |
| `new.author_id = old.author_id` | `old.author_id = new.author_id` | `author_id = ?` (new value) |
| `new.version > old.version` | `old.version < new.version` | `version < ?` (new value) |

**Conditions with only `new.*` and `auth.*`** → Go validation before query

| Condition | Validation |
|-----------|------------|
| `new.author_id = auth.id` | `values["author_id"] == auth.UserID` |
| `new.X = new.Y` | `values["X"] == values["Y"]` |
| `new.price > 0` | `values["price"] > 0` |
| `new.status IN ('draft', 'published')` | `values["status"]` in allowed list |

### WHERE Clause Generation

Any condition involving `old.*` is rearranged so `old.*` is isolated, then converted to a WHERE clause:

```go
func conditionToWhere(cond PolicyCondition, auth *AuthContext, values map[string]any) string {
    // Resolve the comparison value
    var compareValue any
    switch {
    case cond.Value == "auth.id":
        compareValue = auth.UserID
    case cond.Value == "auth.role":
        compareValue = *auth.Role
    case strings.HasPrefix(cond.Value, "new."):
        col := strings.TrimPrefix(cond.Value, "new.")
        compareValue = values[col]
    default:
        compareValue = cond.Value // literal
    }

    col := strings.TrimPrefix(cond.Field, "old.")

    switch cond.Op {
    case "eq":
        return fmt.Sprintf("%s = %s", sqlIdentifier(col), sqlParam(compareValue))
    case "ne":
        return fmt.Sprintf("%s != %s", sqlIdentifier(col), sqlParam(compareValue))
    case "lt":
        return fmt.Sprintf("%s < %s", sqlIdentifier(col), sqlParam(compareValue))
    case "gt":
        return fmt.Sprintf("%s > %s", sqlIdentifier(col), sqlParam(compareValue))
    case "in":
        return fmt.Sprintf("%s IN (%s)", sqlIdentifier(col), sqlParamList(compareValue))
    }
    // ... other operators
}
```

### Evaluating Nested Conditions

For AND/OR/NOT with mixed `old.*`, `new.*`, and `auth.*` conditions, we use three-valued evaluation:

- **pass** — condition fully satisfied by `new.*`/`auth.*` evaluation
- **fail** — condition cannot be satisfied
- **maybe** — has `old.*` conditions that SQL must evaluate

```go
type EvalResult int

const (
    Pass  EvalResult = iota  // Fully satisfied, no WHERE needed
    Fail                      // Cannot be satisfied, deny request
    Maybe                     // Has old.* conditions, add to WHERE
)

type Evaluation struct {
    Result   EvalResult
    WhereSql string      // SQL WHERE clause if Result == Maybe
}

func evaluateCondition(c *Condition, auth *AuthContext, values map[string]any) Evaluation {
    if c.IsLeaf() {
        return evaluateLeaf(c, auth, values)
    }
    if len(c.And) > 0 {
        return evaluateAnd(c.And, auth, values)
    }
    if len(c.Or) > 0 {
        return evaluateOr(c.Or, auth, values)
    }
    if c.Not != nil {
        return evaluateNot(c.Not, auth, values)
    }
    return Evaluation{Result: Pass}
}

func evaluateLeaf(c *Condition, auth *AuthContext, values map[string]any) Evaluation {
    // old.* conditions can't be evaluated in Go — defer to SQL
    if strings.HasPrefix(c.Field, "old.") {
        return Evaluation{
            Result:   Maybe,
            WhereSql: leafToSQL(c, auth, values),
        }
    }

    // new.* and auth.* conditions — evaluate in Go
    if evaluateInGo(c, auth, values) {
        return Evaluation{Result: Pass}
    }
    return Evaluation{Result: Fail}
}

func evaluateAnd(conds []Condition, auth *AuthContext, values map[string]any) Evaluation {
    var whereParts []string

    for _, c := range conds {
        eval := evaluateCondition(&c, auth, values)
        switch eval.Result {
        case Fail:
            return Evaluation{Result: Fail}  // Any fail → AND fails
        case Maybe:
            whereParts = append(whereParts, eval.WhereSql)
        // Pass continues
        }
    }

    if len(whereParts) == 0 {
        return Evaluation{Result: Pass}
    }
    return Evaluation{
        Result:   Maybe,
        WhereSql: "(" + strings.Join(whereParts, " AND ") + ")",
    }
}

func evaluateOr(conds []Condition, auth *AuthContext, values map[string]any) Evaluation {
    var whereParts []string

    for _, c := range conds {
        eval := evaluateCondition(&c, auth, values)
        switch eval.Result {
        case Pass:
            return Evaluation{Result: Pass}  // Any pass → OR passes, no WHERE needed
        case Maybe:
            whereParts = append(whereParts, eval.WhereSql)
        // Fail continues
        }
    }

    if len(whereParts) == 0 {
        return Evaluation{Result: Fail}  // All failed, nothing to try
    }
    return Evaluation{
        Result:   Maybe,
        WhereSql: "(" + strings.Join(whereParts, " OR ") + ")",
    }
}

func evaluateNot(c *Condition, auth *AuthContext, values map[string]any) Evaluation {
    eval := evaluateCondition(c, auth, values)
    switch eval.Result {
    case Pass:
        return Evaluation{Result: Fail}  // NOT(pass) = fail
    case Fail:
        return Evaluation{Result: Pass}  // NOT(fail) = pass
    default:
        return Evaluation{
            Result:   Maybe,
            WhereSql: "NOT(" + eval.WhereSql + ")",
        }
    }
}
```

**Example evaluations:**

```json
// Policy: (old.author_id = auth.id) OR (new.status = 'draft')
{
  "or": [
    {"field": "old.author_id", "op": "eq", "value": "auth.id"},
    {"field": "new.status", "op": "eq", "value": "draft"}
  ]
}
```

Request `{status: "draft"}`:
- Branch 1: `old.*` → Maybe
- Branch 2: `new.status = 'draft'` → Pass
- OR has a Pass → Result: **Pass, no WHERE needed**

Request `{status: "published"}`:
- Branch 1: `old.*` → Maybe with `author_id = ?`
- Branch 2: `new.status = 'draft'` → Fail
- OR has only Maybe → Result: **Maybe, WHERE: `author_id = ?`**

### Request Flow

1. Parse request body
2. **Evaluate condition tree** — recursively evaluate with three-valued logic
3. If Fail → return error immediately
4. If Pass → execute query without RLS WHERE
5. If Maybe → execute query with generated WHERE clause

### System Bypass

System operations (background jobs, migrations, cascading deletes, admin) skip policy enforcement entirely:

```go
func HandleDataRequest(db *sql.DB, req *http.Request, isSystem bool) (*Response, error) {
    auth, err := ResolveRequest(db, req)
    if err != nil {
        return nil, err
    }

    query, err := ParseQuery(req)
    if err != nil {
        return nil, err
    }

    // System operations bypass policy enforcement
    if !isSystem {
        policy, err := LoadAccessPolicy(db, auth.DefinitionID, auth.Version, query.Table, query.Operation)
        if err != nil {
            return nil, err
        }

        if !policy.IsAllow() {
            eval := evaluateCondition(policy.Condition, auth, query.Values)
            switch eval.Result {
            case Fail:
                return nil, ErrOperationNotAllowed
            case Maybe:
                query.InjectWhere(eval.WhereSql)
            // Pass: no WHERE needed
            }
        }
    }

    return ExecuteQuery(auth.DatabaseID, query)
}
```

### SQL Condition Reordering

Evaluating `new.*`/`auth.*` conditions in Go first and then adding `old.*` as WHERE effectively reorders conditions. This is safe because:

- SQL WHERE has no guaranteed evaluation order (optimizer reorders freely)
- No side effects in WHERE conditions
- No short-circuit semantics

The query `WHERE a = 1 AND b = 2` and `WHERE b = 2 AND a = 1` are semantically identical.

### Data Payloads

INSERT/UPDATE data values are always literals (strings, numbers, null). SQL expressions are not supported:

- All values are parameterized for safety
- Validation compares literal values directly
- For computed values like `datetime('now')`, use schema defaults or pass literal from client

### Schema-Level Constraints

Access policies are separate from schema constraints. If you need hard data integrity rules:

- Define CHECK constraints in the schema definition (column-level)
- These are enforced by SQLite regardless of access policies
- System operations still respect schema constraints

### Batch Insert Semantics

When inserting multiple rows, if any row fails policy validation, reject the entire batch. This matches Postgres RLS behavior.

```go
func validateBatchInsert(policy *AccessPolicy, auth *AuthContext, rows []map[string]any) error {
    for i, row := range rows {
        eval := evaluateCondition(policy.Condition, auth, row)
        if eval.Result == Fail {
            return fmt.Errorf("row %d: %w", i, ErrOperationNotAllowed)
        }
        // Note: Maybe results for INSERT would be unusual (old.* not valid for INSERT)
        // but if present, they would all generate the same WHERE since there's no old row
    }
    return nil
}
```

**Behavior:**
- Validate all rows before executing any INSERT
- If any row fails → return error, no rows inserted
- Atomic: all or nothing
- Consistent with Postgres RLS batch behavior

## Layer 2: Policy Loading

Load policies from `atombase_access_policies` with granular caching per table/operation.

**Cache keys:**
- Access: `access:{definition_id}:{version}:{table}:{operation}`
- Management: `mgmt:{definition_id}:{role}:{action}`

**Policy logic:**
1. No row exists → deny (operation not configured)
2. Row exists, `conditions_json` empty → allow all
3. Row exists, `conditions_json` has content → parse and apply RLS

```go
var policyCache = sync.Map{}  // map[string]*AccessPolicy

func LoadAccessPolicy(db *sql.DB, defID, version int, table string, op Operation) (*AccessPolicy, error) {
    cacheKey := fmt.Sprintf("access:%d:%d:%s:%s", defID, version, table, op)

    // Check cache first
    if cached, ok := policyCache.Load(cacheKey); ok {
        return cached.(*AccessPolicy), nil
    }

    // Load from normalized table
    var conditionsJSON sql.NullString
    err := db.QueryRow(`
        SELECT conditions_json
        FROM atombase_access_policies
        WHERE definition_id = ? AND version = ? AND table_name = ? AND operation = ?
    `, defID, version, table, op).Scan(&conditionsJSON)

    if err == sql.ErrNoRows {
        return nil, ErrOperationNotAllowed  // No policy = denied
    }
    if err != nil {
        return nil, err
    }

    policy := &AccessPolicy{}
    if conditionsJSON.Valid && conditionsJSON.String != "" {
        policy.Conditions = parseConditions(conditionsJSON.String)
    }

    policyCache.Store(cacheKey, policy)
    return policy, nil
}

func parseConditions(json string) []PolicyCondition {
    // Parse JSON array of conditions
    // e.g., [{"field": "old.author_id", "op": "eq", "value": "auth.id"}]
    var conditions []PolicyCondition
    json.Unmarshal([]byte(json), &conditions)
    return conditions
}
```

### Loading Policies for Joins (Batch)

When a SELECT joins multiple tables, batch load policies for efficiency:

```go
func LoadPoliciesForTables(db *sql.DB, defID, version int, tables []string, op Operation) (map[string]*AccessPolicy, error) {
    policies := make(map[string]*AccessPolicy)

    // Check cache first, collect uncached tables
    var uncached []string
    for _, table := range tables {
        cacheKey := fmt.Sprintf("access:%d:%d:%s:%s", defID, version, table, op)
        if cached, ok := policyCache.Load(cacheKey); ok {
            policies[table] = cached.(*AccessPolicy)
        } else {
            uncached = append(uncached, table)
        }
    }

    if len(uncached) == 0 {
        return policies, nil
    }

    // Batch load uncached policies in single query
    placeholders := strings.Repeat("?,", len(uncached))
    placeholders = placeholders[:len(placeholders)-1]

    args := []any{defID, version, string(op)}
    for _, t := range uncached {
        args = append(args, t)
    }

    rows, err := db.Query(fmt.Sprintf(`
        SELECT table_name, conditions_json
        FROM atombase_access_policies
        WHERE definition_id = ? AND version = ? AND operation = ?
        AND table_name IN (%s)
    `, placeholders), args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    found := make(map[string]bool)
    for rows.Next() {
        var tableName string
        var conditionsJSON sql.NullString
        rows.Scan(&tableName, &conditionsJSON)

        policy := &AccessPolicy{}
        if conditionsJSON.Valid && conditionsJSON.String != "" {
            policy.Conditions = parseConditions(conditionsJSON.String)
        }

        cacheKey := fmt.Sprintf("access:%d:%d:%s:%s", defID, version, tableName, op)
        policyCache.Store(cacheKey, policy)
        policies[tableName] = policy
        found[tableName] = true
    }

    // Check all tables have policies (no policy = denied)
    for _, table := range uncached {
        if !found[table] {
            return nil, fmt.Errorf("table %s: %w", table, ErrOperationNotAllowed)
        }
    }

    return policies, nil
}
```

## Layer 3: Operation Authorization

With normalized policies, authorization is implicit in policy loading. If `LoadAccessPolicy` returns a policy, the operation is allowed (with optional RLS conditions).

```go
func AuthorizeAndGetPolicy(db *sql.DB, auth *AuthContext, table string, op Operation) (*AccessPolicy, error) {
    policy, err := LoadAccessPolicy(db, auth.DefinitionID, auth.Version, table, op)
    if err != nil {
        return nil, err
    }

    // Check role-based conditions (for org databases)
    for _, cond := range policy.Conditions {
        if cond.Field == "auth.role" {
            if auth.Role == nil {
                return nil, ErrOperationNotAllowed
            }
            if !matchesRoleCondition(cond, *auth.Role) {
                return nil, ErrOperationNotAllowed
            }
        }
    }

    return policy, nil
}
```

## Example Flows

### Example 1: Global Database Select

```
Request:
  POST /data/query/posts
  Authorization: Bearer session-123
  Database: global:marketplace

Policy (from atombase_access_policies):
  definition_id=1, version=1, table_name='posts', operation='select'
  conditions_json=NULL  (r.allow())

Flow:
  1. Parse header → type=global, name=marketplace
  2. Resolve auth → user-456, database info
  3. Load policy → conditions empty (allow)
  4. No validation or WHERE injection needed
  5. Execute: SELECT * FROM posts
```

### Example 2: User Database Insert with new.* Validation

```
Request:
  POST /data/query/notes
  Authorization: Bearer session-123
  Database: user:notes
  Body: { "content": "Hello", "user_id": "user-456" }

Policy:
  conditions_json='[{"field":"new.user_id","op":"eq","value":"auth.id"}]'

Flow:
  1. Parse header → type=user
  2. Resolve auth → user-456, database info
  3. Load policy → has new.user_id = auth.id condition
  4. Validate in Go: values["user_id"] ("user-456") == auth.id ("user-456") ✓
  5. Execute: INSERT INTO notes (content, user_id) VALUES (?, ?)
```

### Example 3: Org Database Delete with old.* WHERE

```
Request:
  DELETE /data/query/posts
  Authorization: Bearer session-123
  Database: org:acme-corp
  Body: { "where": [{"id": {"eq": 123}}] }

Policy:
  conditions_json='[{"field":"old.author_id","op":"eq","value":"auth.id"}]'

Flow:
  1. Parse header → type=org, name=acme-corp
  2. Resolve auth → user-456, role="member", database info
  3. Load policy → has old.author_id = auth.id condition
  4. Build WHERE from old.* condition: author_id = 'user-456'
  5. Execute: DELETE FROM posts WHERE id = 123 AND author_id = 'user-456'
  6. If row exists but author_id != user-456 → 0 rows affected (silent denial)
```

### Example 4: Update with Immutability (new.X = old.X)

```
Request:
  PATCH /data/query/posts
  Authorization: Bearer session-123
  Database: org:acme-corp
  Body: { "data": {"title": "New Title", "author_id": "user-456"}, "where": [{"id": {"eq": 123}}] }

Policy:
  conditions_json='[
    {"field":"old.author_id","op":"eq","value":"auth.id"},
    {"field":"new.author_id","op":"eq","value":"old.author_id"}
  ]'

Flow:
  1. Resolve auth → user-456
  2. Load policy → two conditions
  3. Condition 1 (old.author_id = auth.id): WHERE author_id = 'user-456'
  4. Condition 2 (new.author_id = old.author_id): rearrange to old.author_id = new.author_id
     → WHERE author_id = 'user-456' (the new value being set)
  5. Combined WHERE: author_id = 'user-456' AND author_id = 'user-456'
  6. Execute: UPDATE posts SET title = ?, author_id = ? WHERE id = 123 AND author_id = 'user-456'
  7. If user tries to change author_id to different value → 0 rows affected
```

### Example 5: Select with Joins

```
Request:
  POST /data/query/posts
  Authorization: Bearer session-123
  Database: org:acme-corp
  Body: {
    "select": ["id", "title", {"comments": ["id", "text"]}],
    "where": [{"id": {"eq": 123}}]
  }

Policies:
  posts.select: conditions_json=NULL (allow)
  comments.select: conditions_json='[{"field":"old.visible","op":"eq","value":true}]'

Flow:
  1. Resolve auth → user-456, role="member"
  2. Load policies for [posts, comments]
  3. posts: allow (no conditions)
  4. comments: build WHERE from old.visible = true
  5. Execute with WHERE on comments subquery
```

### Example 6: Version Increment (new.X > old.X)

```
Request:
  PATCH /data/query/documents
  Authorization: Bearer session-123
  Database: user:docs
  Body: { "data": {"content": "Updated", "version": 5}, "where": [{"id": {"eq": 1}}] }

Policy:
  conditions_json='{"field":"new.version","op":"gt","value":"old.version"}'

Flow:
  1. Resolve auth → user-456
  2. Load policy → new.version > old.version
  3. Rearrange: old.version < new.version → WHERE version < 5
  4. Execute: UPDATE documents SET content = ?, version = 5 WHERE id = 1 AND version < 5
  5. If current version >= 5 → 0 rows affected (stale update rejected)
```

### Example 7: OR with Mixed old.*/new.* (Author OR Draft)

```
Request:
  PATCH /data/query/posts
  Authorization: Bearer session-123
  Database: org:acme-corp
  Body: { "data": {"title": "New Title", "status": "draft"}, "where": [{"id": {"eq": 123}}] }

Policy:
  conditions_json='{
    "or": [
      {"field": "old.author_id", "op": "eq", "value": "auth.id"},
      {"field": "new.status", "op": "eq", "value": "draft"}
    ]
  }'

Flow (status = "draft"):
  1. Resolve auth → user-456
  2. Load policy → OR of two conditions
  3. Evaluate OR:
     - Branch 1: old.author_id = auth.id → Maybe (has old.*)
     - Branch 2: new.status = 'draft' → Pass (new.* matches)
  4. OR has a Pass → Result: Pass, no WHERE needed
  5. Execute: UPDATE posts SET title = ?, status = ? WHERE id = 123

Flow (status = "published"):
  1. Resolve auth → user-456
  2. Load policy → OR of two conditions
  3. Evaluate OR:
     - Branch 1: old.author_id = auth.id → Maybe with WHERE author_id = 'user-456'
     - Branch 2: new.status = 'draft' → Fail (new.status is 'published')
  4. OR has only Maybe → Result: Maybe, WHERE: author_id = 'user-456'
  5. Execute: UPDATE posts SET title = ?, status = ? WHERE id = 123 AND author_id = 'user-456'
```

### Example 8: Nested AND/OR (Admin Draft OR Author Anything)

```
Request:
  DELETE /data/query/posts
  Authorization: Bearer session-123
  Database: org:acme-corp
  Body: { "where": [{"id": {"eq": 123}}] }

Policy:
  conditions_json='{
    "or": [
      {"field": "old.author_id", "op": "eq", "value": "auth.id"},
      {
        "and": [
          {"field": "auth.role", "op": "eq", "value": "admin"},
          {"field": "old.status", "op": "eq", "value": "draft"}
        ]
      }
    ]
  }'

Flow (user is admin):
  1. Resolve auth → user-456, role="admin"
  2. Evaluate OR:
     - Branch 1: old.author_id = auth.id → Maybe
     - Branch 2 (AND):
       - auth.role = 'admin' → Pass
       - old.status = 'draft' → Maybe
       - AND: one Pass, one Maybe → Maybe with WHERE status = 'draft'
  3. OR: both branches are Maybe → combine with OR
  4. Result: Maybe, WHERE: (author_id = 'user-456') OR (status = 'draft')
  5. Execute: DELETE FROM posts WHERE id = 123 AND ((author_id = 'user-456') OR (status = 'draft'))

Flow (user is member, is author):
  1. Resolve auth → user-456, role="member"
  2. Evaluate OR:
     - Branch 1: old.author_id = auth.id → Maybe with WHERE author_id = 'user-456'
     - Branch 2 (AND):
       - auth.role = 'admin' → Fail (user is member)
       - AND has Fail → Fail
  3. OR: one Maybe, one Fail → Maybe
  4. Result: Maybe, WHERE: author_id = 'user-456'
  5. Execute: DELETE FROM posts WHERE id = 123 AND author_id = 'user-456'
```

## Cache Management

Policy cache is rebuilt on startup and invalidated when definitions change.

```go
// RebuildPolicyCache loads all policies into cache on startup
func RebuildPolicyCache(db *sql.DB) error {
    rows, err := db.Query(`
        SELECT definition_id, version, table_name, operation, conditions_json
        FROM atombase_access_policies
    `)
    if err != nil {
        return err
    }
    defer rows.Close()

    for rows.Next() {
        var defID, version int
        var tableName, operation string
        var conditionsJSON sql.NullString
        rows.Scan(&defID, &version, &tableName, &operation, &conditionsJSON)

        policy := &AccessPolicy{}
        if conditionsJSON.Valid && conditionsJSON.String != "" {
            policy.Conditions = parseConditions(conditionsJSON.String)
        }

        cacheKey := fmt.Sprintf("access:%d:%d:%s:%s", defID, version, tableName, operation)
        policyCache.Store(cacheKey, policy)
    }
    return nil
}

// InvalidatePolicyCacheForDefinition clears cache when definition is updated
// Called by platform API on definition push
func InvalidatePolicyCacheForDefinition(defID int) {
    prefix := fmt.Sprintf("access:%d:", defID)
    policyCache.Range(func(key, value any) bool {
        if strings.HasPrefix(key.(string), prefix) {
            policyCache.Delete(key)
        }
        return true
    })
}
```

## Role Validation

Validate roles at write time (membership insert/update) to prevent invalid data.

```go
// ValidateRole checks that a role exists in the definition's roles_json
// Call this before creating or updating membership
func ValidateRole(db *sql.DB, defID int, role string) error {
    var rolesJSON sql.NullString
    err := db.QueryRow(`
        SELECT roles_json FROM atombase_definitions WHERE id = ?
    `, defID).Scan(&rolesJSON)
    if err != nil {
        return err
    }

    if !rolesJSON.Valid || rolesJSON.String == "" {
        return ErrInvalidRole  // Definition has no roles (global/user type)
    }

    var roles []string
    json.Unmarshal([]byte(rolesJSON.String), &roles)

    for _, r := range roles {
        if r == role {
            return nil
        }
    }
    return ErrInvalidRole
}

// Usage in membership creation:
func AddMember(db *sql.DB, orgID, userID, role string) error {
    // Get definition ID for this org
    var defID int
    db.QueryRow(`
        SELECT d.definition_id
        FROM atombase_organizations o
        JOIN atombase_databases d ON d.id = o.database_id
        WHERE o.id = ?
    `, orgID).Scan(&defID)

    // Validate role before inserting
    if err := ValidateRole(db, defID, role); err != nil {
        return err
    }

    _, err := db.Exec(`
        INSERT INTO atombase_membership (organization_id, user_id, role)
        VALUES (?, ?, ?)
    `, orgID, userID, role)
    return err
}
```

## Error Handling

```go
var (
    ErrUnauthenticated       = &APIError{Code: 401, Message: "authentication required"}
    ErrInvalidToken          = &APIError{Code: 401, Message: "invalid token format"}
    ErrInvalidSession        = &APIError{Code: 401, Message: "invalid session"}
    ErrSessionExpired        = &APIError{Code: 401, Message: "session expired"}
    ErrSessionInactive       = &APIError{Code: 401, Message: "session inactive"}
    ErrInvalidDatabaseHeader = &APIError{Code: 400, Message: "invalid Database header format"}
    ErrInvalidDatabaseType   = &APIError{Code: 400, Message: "invalid database type"}
    ErrInvalidRole           = &APIError{Code: 400, Message: "invalid role for this definition"}
    ErrNotMember             = &APIError{Code: 403, Message: "not a member of this organization"}
    ErrNoUserDatabase        = &APIError{Code: 404, Message: "user has no database"}
    ErrDatabaseNotFound      = &APIError{Code: 404, Message: "database not found"}
    ErrOperationNotAllowed   = &APIError{Code: 403, Message: "operation not allowed"}
)

type PolicyViolationError struct {
    Field    string
    Expected any
    Actual   any
}

func (e *PolicyViolationError) Error() string {
    return fmt.Sprintf("policy violation: %s expected %v, got %v", e.Field, e.Expected, e.Actual)
}
```

## Performance Considerations

1. **Session writes minimized** — Update `last_verified_at` at most once per hour, not per request
2. **Constant-time hash comparison** — Prevents timing attacks on session secrets
3. **Separate session validation** — Session validated once, then userID passed to resolve functions
4. **Granular policy cache** — Cache by `definition:version:table:operation`, rebuilt on startup
5. **Batch policy loading** — Single query loads all table policies for joins
6. **Cache invalidation on push** — Platform API invalidates cache when definitions change
7. **Role validation at write time** — Invalid roles rejected on membership creation, not query time
8. **No session join in resolve** — Session already validated, resolve functions only query necessary tables
9. **Pure runtime enforcement** — No triggers or CHECKs from access policies, all enforcement in API layer
10. **System bypass** — System operations skip policy enforcement entirely, no overhead
