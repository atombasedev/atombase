package definitions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/auth"
	"github.com/atombasedev/atombase/tools"
)

type Store interface {
	ResolveDatabaseTarget(ctx context.Context, principal Principal, header string) (DatabaseTarget, error)
	LoadAccessPolicy(ctx context.Context, definitionID int32, version int, table, operation string) (*AccessPolicy, error)
	DB() *sql.DB
}

type Service struct {
	store    Store
	compiler *Compiler
}

func NewService(store Store) *Service {
	return &Service{store: store, compiler: NewCompiler()}
}

func (s *Service) ResolvePrincipal(ctx context.Context, authCtx tools.AuthContext) (Principal, error) {
	switch authCtx.Role {
	case tools.RoleService:
		return Principal{
			AuthStatus: AuthStatusAuthenticated,
			IsService:  true,
		}, nil
	case tools.RoleAnonymous:
		return Principal{AuthStatus: AuthStatusAnonymous}, nil
	case tools.RoleUser:
		if s == nil || s.store == nil || s.store.DB() == nil {
			return Principal{}, errors.New("primary store not initialized")
		}
		session, err := auth.ValidateSession(auth.SessionToken(authCtx.Token), s.store.DB(), ctx)
		if err != nil {
			return Principal{}, tools.UnauthorizedErr("invalid session")
		}
		return Principal{
			UserID:     session.UserID,
			SessionID:  session.Id,
			AuthStatus: AuthStatusAuthenticated,
		}, nil
	default:
		return Principal{}, tools.UnauthorizedErr("unsupported auth role")
	}
}

func (s *Service) ResolveTarget(ctx context.Context, principal Principal, header string) (DatabaseTarget, error) {
	if s == nil || s.store == nil {
		return DatabaseTarget{}, errors.New("definitions service not initialized")
	}
	return s.store.ResolveDatabaseTarget(ctx, principal, header)
}

func (s *Service) CompilePolicy(ctx context.Context, principal Principal, target DatabaseTarget, table, operation string, newValues map[string]any) (CompiledPredicate, error) {
	policy, err := s.store.LoadAccessPolicy(ctx, target.DefinitionID, target.DefinitionVersion, table, operation)
	if err != nil {
		return CompiledPredicate{}, err
	}
	return s.compiler.Compile(policy, CompileInput{
		Principal: principal,
		Target:    target,
		Table:     table,
		Operation: operation,
		NewValues: newValues,
	})
}

func ParseAndValidateAccess(defType DefinitionType, raw AccessMap, schemaTables map[string]struct{}) ([]AccessPolicy, error) {
	var rows []AccessPolicy
	for table, ops := range raw {
		if _, ok := schemaTables[table]; !ok {
			return nil, fmt.Errorf("access policy references unknown table %q", table)
		}
		for _, item := range []struct {
			op   string
			cond *Condition
		}{
			{op: "select", cond: ops.Select},
			{op: "insert", cond: ops.Insert},
			{op: "update", cond: ops.Update},
			{op: "delete", cond: ops.Delete},
		} {
			if item.cond == nil {
				continue
			}
			if err := ValidateConditionContext(*item.cond, item.op, defType); err != nil {
				return nil, err
			}
			cond := *item.cond
			rows = append(rows, AccessPolicy{
				Table:     table,
				Operation: item.op,
				Condition: &cond,
			})
		}
	}
	return rows, nil
}

func ParseAndValidateManagement(defType DefinitionType, roles []string, raw ManagementMap) ([]ManagementRule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if defType != DefinitionTypeOrganization {
		return nil, fmt.Errorf("management policies are only valid for organization definitions")
	}
	roleSet := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		roleSet[role] = struct{}{}
	}
	var rows []ManagementRule
	for role, policy := range raw {
		if _, ok := roleSet[role]; !ok {
			return nil, fmt.Errorf("management policy references unknown role %q", role)
		}
		for _, item := range []struct {
			action ManagementAction
			perm   ManagementPermission
		}{
			{action: ManagementActionInvite, perm: policy.Invite},
			{action: ManagementActionAssignRole, perm: policy.AssignRole},
			{action: ManagementActionRemoveMember, perm: policy.RemoveMember},
		} {
			if !item.perm.Allowed {
				continue
			}
			if !item.perm.Any {
				for _, targetRole := range item.perm.Roles {
					if _, ok := roleSet[targetRole]; !ok {
						return nil, fmt.Errorf("management policy for role %q references unknown target role %q", role, targetRole)
					}
				}
			}
			rows = append(rows, ManagementRule{
				Role:        role,
				Action:      item.action,
				TargetRoles: append([]string(nil), item.perm.Roles...),
			})
		}
		for _, item := range []struct {
			action  ManagementAction
			allowed bool
		}{
			{action: ManagementActionUpdateOrg, allowed: policy.UpdateOrg},
			{action: ManagementActionDeleteOrg, allowed: policy.DeleteOrg},
			{action: ManagementActionTransferOwnership, allowed: policy.TransferOwnership},
		} {
			if !item.allowed {
				continue
			}
			rows = append(rows, ManagementRule{
				Role:   role,
				Action: item.action,
			})
		}
	}
	return rows, nil
}

func ValidateConditionContext(cond Condition, op string, defType DefinitionType) error {
	if cond.Field != "" {
		if strings.HasPrefix(cond.Field, "old.") && op == "insert" {
			return fmt.Errorf("old.* not valid for insert policies")
		}
		if strings.HasPrefix(cond.Field, "new.") && (op == "select" || op == "delete") {
			return fmt.Errorf("new.* not valid for %s policies", op)
		}
		if (cond.Field == "auth.role" || cond.Value == "auth.role") && defType != DefinitionTypeOrganization {
			return fmt.Errorf("auth.role only valid for organization definitions")
		}
		return nil
	}
	for _, child := range cond.And {
		if err := ValidateConditionContext(child, op, defType); err != nil {
			return err
		}
	}
	for _, child := range cond.Or {
		if err := ValidateConditionContext(child, op, defType); err != nil {
			return err
		}
	}
	if cond.Not != nil {
		return ValidateConditionContext(*cond.Not, op, defType)
	}
	return nil
}

func DecodeCondition(raw string) (*Condition, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var cond Condition
	if err := json.Unmarshal([]byte(raw), &cond); err != nil {
		return nil, err
	}
	return &cond, nil
}
