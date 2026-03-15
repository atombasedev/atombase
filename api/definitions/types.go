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

type ManagementAction string

const (
	ManagementActionInvite            ManagementAction = "invite"
	ManagementActionAssignRole        ManagementAction = "assignRole"
	ManagementActionRemoveMember      ManagementAction = "removeMember"
	ManagementActionUpdateOrg         ManagementAction = "updateOrg"
	ManagementActionDeleteOrg         ManagementAction = "deleteOrg"
	ManagementActionTransferOwnership ManagementAction = "transferOwnership"
)

type ManagementPermission struct {
	Allowed bool     `json:"-"`
	Any     bool     `json:"any,omitempty"`
	Roles   []string `json:"roles,omitempty"`
}

func (p *ManagementPermission) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = ManagementPermission{}
		return nil
	}
	var boolVal bool
	if err := json.Unmarshal(data, &boolVal); err == nil {
		p.Allowed = boolVal
		p.Any = false
		p.Roles = nil
		return nil
	}
	var roles []string
	if err := json.Unmarshal(data, &roles); err == nil {
		p.Allowed = true
		p.Any = false
		p.Roles = roles
		return nil
	}
	var shape struct {
		Any bool `json:"any"`
	}
	if err := json.Unmarshal(data, &shape); err == nil && shape.Any {
		p.Allowed = true
		p.Any = true
		p.Roles = nil
		return nil
	}
	return json.Unmarshal(data, &struct{}{})
}

func (p ManagementPermission) MarshalJSON() ([]byte, error) {
	if !p.Allowed {
		return json.Marshal(false)
	}
	if p.Any {
		return json.Marshal(map[string]bool{"any": true})
	}
	if len(p.Roles) > 0 {
		return json.Marshal(p.Roles)
	}
	return json.Marshal(true)
}

func (p ManagementPermission) Allows(targetRole string) bool {
	if !p.Allowed {
		return false
	}
	if p.Any {
		return true
	}
	if len(p.Roles) == 0 {
		return true
	}
	for _, role := range p.Roles {
		if role == targetRole {
			return true
		}
	}
	return false
}

type ManagementPolicy struct {
	Invite            ManagementPermission `json:"invite,omitempty"`
	AssignRole        ManagementPermission `json:"assignRole,omitempty"`
	RemoveMember      ManagementPermission `json:"removeMember,omitempty"`
	UpdateOrg         bool                 `json:"updateOrg,omitempty"`
	DeleteOrg         bool                 `json:"deleteOrg,omitempty"`
	TransferOwnership bool                 `json:"transferOwnership,omitempty"`
}

type ManagementMap map[string]ManagementPolicy

type ManagementRule struct {
	DefinitionID int32
	Role         string
	Action       ManagementAction
	TargetRoles  []string
}

type Definition struct {
	ID             int32           `json:"id"`
	Name           string          `json:"name"`
	Type           DefinitionType  `json:"type"`
	Roles          []string        `json:"roles,omitempty"`
	Management     ManagementMap   `json:"management,omitempty"`
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
	Name       string          `json:"name"`
	Type       DefinitionType  `json:"type"`
	Roles      []string        `json:"roles,omitempty"`
	Management ManagementMap   `json:"management,omitempty"`
	Schema     json.RawMessage `json:"schema"`
	Access     AccessMap       `json:"access"`
}

type PushDefinitionRequest struct {
	Schema     json.RawMessage `json:"schema"`
	Access     AccessMap       `json:"access"`
	Management ManagementMap   `json:"management,omitempty"`
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
