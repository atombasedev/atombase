package definitions

import "encoding/json"

type DefinitionType string

const (
	DefinitionTypeGlobal       DefinitionType = "global"
	DefinitionTypeOrganization DefinitionType = "organization"
	DefinitionTypeUser         DefinitionType = "user"
)

type AuthStatus string

const (
	AuthStatusAnonymous     AuthStatus = "anonymous"
	AuthStatusAuthenticated AuthStatus = "authenticated"
)

type Principal struct {
	UserID     string
	SessionID  string
	AuthStatus AuthStatus
	IsService  bool
}

type DatabaseTarget struct {
	DatabaseID        string
	DefinitionID      int32
	DefinitionName    string
	DefinitionType    DefinitionType
	DefinitionVersion int
	AuthToken         string
}

type Condition struct {
	Field string `json:"field,omitempty"`
	Op    string `json:"op,omitempty"`
	Value any    `json:"value,omitempty"`

	And []Condition `json:"and,omitempty"`
	Or  []Condition `json:"or,omitempty"`
	Not *Condition  `json:"not,omitempty"`
}

func (c Condition) IsZero() bool {
	return c.Field == "" && len(c.And) == 0 && len(c.Or) == 0 && c.Not == nil
}

type AccessPolicy struct {
	DefinitionID int32
	Version      int
	Table        string
	Operation    string
	Condition    *Condition
}

type Definition struct {
	ID             int32           `json:"id"`
	Name           string          `json:"name"`
	Type           DefinitionType  `json:"type"`
	Roles          []string        `json:"roles,omitempty"`
	CurrentVersion int             `json:"currentVersion"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
	Schema         json.RawMessage `json:"schema,omitempty"`
}

type DefinitionVersion struct {
	ID           int32           `json:"id"`
	DefinitionID int32           `json:"definitionId"`
	Version      int             `json:"version"`
	Schema       json.RawMessage `json:"schema"`
	Checksum     string          `json:"checksum"`
	CreatedAt    string          `json:"createdAt"`
}

type CreateDefinitionRequest struct {
	Name   string          `json:"name"`
	Type   DefinitionType  `json:"type"`
	Roles  []string        `json:"roles,omitempty"`
	Schema json.RawMessage `json:"schema"`
	Access AccessMap       `json:"access"`
}

type PushDefinitionRequest struct {
	Schema json.RawMessage `json:"schema"`
	Access AccessMap       `json:"access"`
}

type CreateDatabaseRequest struct {
	ID               string `json:"id"`
	Definition       string `json:"definition"`
	UserID           string `json:"userId,omitempty"`
	OrganizationID   string `json:"organizationId,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
	OwnerID          string `json:"ownerId,omitempty"`
	MaxMembers       *int   `json:"maxMembers,omitempty"`
}

type AccessMap map[string]OperationPolicy

type OperationPolicy struct {
	Select *Condition `json:"select,omitempty"`
	Insert *Condition `json:"insert,omitempty"`
	Update *Condition `json:"update,omitempty"`
	Delete *Condition `json:"delete,omitempty"`
}

type CompiledPredicate struct {
	SQL                string
	Args               []any
	GoAllowed          bool
	NeedsMembershipCTE bool
}
