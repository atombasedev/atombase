package definitions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/tools"
)

type CompileInput struct {
	Principal Principal
	Target    DatabaseTarget
	Table     string
	Operation string
	NewValues map[string]any
	NewAlias  string
}

type Compiler struct{}

func NewCompiler() *Compiler {
	return &Compiler{}
}

func (c *Compiler) Compile(policy *AccessPolicy, input CompileInput) (CompiledPredicate, error) {
	if input.Principal.IsService {
		return CompiledPredicate{GoAllowed: true}, nil
	}
	if policy == nil {
		return CompiledPredicate{}, tools.InvalidRequestErr("operation is not allowed by definition policy")
	}
	if policy.Condition == nil || policy.Condition.IsZero() {
		return CompiledPredicate{GoAllowed: true}, nil
	}
	sqlExpr, args, allowed, needsMembership, err := compileCondition(*policy.Condition, input)
	if err != nil {
		return CompiledPredicate{}, err
	}
	if !allowed {
		return CompiledPredicate{}, tools.UnauthorizedErr("request does not satisfy definition policy")
	}
	return CompiledPredicate{SQL: sqlExpr, Args: args, GoAllowed: true, NeedsMembershipCTE: needsMembership}, nil
}

func compileCondition(cond Condition, input CompileInput) (string, []any, bool, bool, error) {
	switch {
	case cond.Field != "":
		return compileLeaf(cond, input)
	case len(cond.And) > 0:
		parts := make([]string, 0, len(cond.And))
		args := make([]any, 0)
		needsMembership := false
		for _, child := range cond.And {
			partSQL, partArgs, allowed, partNeedsMembership, err := compileCondition(child, input)
			if err != nil {
				return "", nil, false, false, err
			}
			if !allowed {
				return "", nil, false, false, nil
			}
			needsMembership = needsMembership || partNeedsMembership
			if partSQL != "" {
				parts = append(parts, partSQL)
				args = append(args, partArgs...)
			}
		}
		if len(parts) == 0 {
			return "", nil, true, needsMembership, nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", args, true, needsMembership, nil
	case len(cond.Or) > 0:
		parts := make([]string, 0, len(cond.Or))
		args := make([]any, 0)
		needsMembership := false
		for _, child := range cond.Or {
			partSQL, partArgs, allowed, partNeedsMembership, err := compileCondition(child, input)
			if err != nil {
				return "", nil, false, false, err
			}
			if !allowed {
				continue
			}
			needsMembership = needsMembership || partNeedsMembership
			if partSQL == "" {
				return "", nil, true, needsMembership, nil
			}
			parts = append(parts, partSQL)
			args = append(args, partArgs...)
		}
		if len(parts) == 0 {
			return "", nil, false, needsMembership, nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", args, true, needsMembership, nil
	case cond.Not != nil:
		partSQL, args, allowed, needsMembership, err := compileCondition(*cond.Not, input)
		if err != nil {
			return "", nil, false, false, err
		}
		if !allowed {
			return "", nil, true, needsMembership, nil
		}
		if partSQL == "" {
			return "", nil, false, needsMembership, nil
		}
		return "(NOT " + partSQL + ")", args, true, needsMembership, nil
	default:
		return "", nil, true, false, nil
	}
}

func compileLeaf(cond Condition, input CompileInput) (string, []any, bool, bool, error) {
	fieldScope, fieldName, err := splitScopedRef(cond.Field)
	if err != nil {
		return "", nil, false, false, err
	}

	switch fieldScope {
	case "auth":
		return compileAuthLeaf(fieldName, cond.Op, cond.Value, input)
	case "new":
		if input.NewAlias != "" {
			return compileNewLeaf(fieldName, cond.Op, cond.Value, input)
		}
		ok, err := evalNewLeaf(fieldName, cond.Op, cond.Value, input)
		return "", nil, ok, false, err
	case "old":
		return compileOldLeaf(fieldName, cond.Op, cond.Value, input)
	default:
		return "", nil, false, false, fmt.Errorf("unsupported policy scope %q", fieldScope)
	}
}

func compileAuthLeaf(fieldName, op string, raw any, input CompileInput) (string, []any, bool, bool, error) {
	switch fieldName {
	case "id":
		ok, err := compareValues(input.Principal.UserID, op, resolveValue(raw, input))
		return "", nil, ok, false, err
	case "status":
		if input.Target.DefinitionType == DefinitionTypeOrganization {
			want := fmt.Sprint(resolveValue(raw, input))
			switch want {
			case "member":
				return "EXISTS (SELECT 1 FROM __ab_membership m WHERE m.user_id = ?)", []any{input.Principal.UserID}, true, true, nil
			case "anonymous":
				return "NOT EXISTS (SELECT 1 FROM __ab_membership m WHERE m.user_id = ?)", []any{input.Principal.UserID}, true, true, nil
			}
		}
		ok, err := compareValues(string(input.Principal.AuthStatus), op, resolveValue(raw, input))
		return "", nil, ok, false, err
	case "role":
		if input.Target.DefinitionType != DefinitionTypeOrganization {
			return "", nil, false, false, errors.New("auth.role is only valid for organization definitions")
		}
		if op == "in" {
			rawList, ok := resolveValue(raw, input).([]any)
			if !ok {
				return "", nil, false, false, fmt.Errorf("auth.role in requires array")
			}
			if len(rawList) == 0 {
				return "", nil, false, true, nil
			}
			placeholders := strings.TrimRight(strings.Repeat("?,", len(rawList)), ",")
			args := append([]any{input.Principal.UserID}, rawList...)
			return "EXISTS (SELECT 1 FROM __ab_membership m WHERE m.user_id = ? AND m.role IN (" + placeholders + "))", args, true, true, nil
		}
		return "EXISTS (SELECT 1 FROM __ab_membership m WHERE m.user_id = ? AND m.role " + sqlOperator(op) + " ?)", []any{input.Principal.UserID, resolveValue(raw, input)}, true, true, nil
	default:
		return "", nil, false, false, fmt.Errorf("unsupported auth field %q", fieldName)
	}
}

func evalNewLeaf(fieldName, op string, raw any, input CompileInput) (bool, error) {
	if input.NewValues == nil {
		return false, nil
	}
	value, ok := input.NewValues[fieldName]
	if !ok {
		return false, nil
	}
	return compareValues(value, op, resolveValue(raw, input))
}

func compileNewLeaf(fieldName, op string, raw any, input CompileInput) (string, []any, bool, bool, error) {
	if err := tools.ValidateIdentifier(fieldName); err != nil {
		return "", nil, false, false, err
	}

	lhs := fmt.Sprintf("%s.[%s]", input.NewAlias, fieldName)

	if input.Target.DefinitionType == DefinitionTypeOrganization {
		if value, ok := raw.(string); ok {
			if value == "auth.role" {
				operator := sqlOperator(op)
				sqlExpr := fmt.Sprintf("EXISTS (SELECT 1 FROM __ab_membership m WHERE m.role %s %s.[%s] AND m.user_id = ?)", operator, input.NewAlias, fieldName)
				return sqlExpr, []any{input.Principal.UserID}, true, true, nil
			}
			if value == "auth.id" {
				return fmt.Sprintf("%s %s ?", lhs, sqlOperator(op)), []any{input.Principal.UserID}, true, false, nil
			}
		}
	}

	if op == "is" || op == "is_not" {
		return fmt.Sprintf("%s %s NULL", lhs, sqlOperator(op)), nil, true, false, nil
	}

	if op == "in" {
		list, ok := resolveValue(raw, input).([]any)
		if !ok {
			return "", nil, false, false, fmt.Errorf("in operator requires array value")
		}
		if len(list) == 0 {
			return "", nil, false, false, nil
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(list)), ",")
		args := make([]any, 0, len(list))
		for _, item := range list {
			args = append(args, resolveValue(item, input))
		}
		return fmt.Sprintf("%s IN (%s)", lhs, placeholders), args, true, false, nil
	}

	if ref, ok := raw.(string); ok && strings.HasPrefix(ref, "new.") {
		otherField := strings.TrimPrefix(ref, "new.")
		if err := tools.ValidateIdentifier(otherField); err != nil {
			return "", nil, false, false, err
		}
		return fmt.Sprintf("%s %s %s.[%s]", lhs, sqlOperator(op), input.NewAlias, otherField), nil, true, false, nil
	}

	return fmt.Sprintf("%s %s ?", lhs, sqlOperator(op)), []any{resolveValue(raw, input)}, true, false, nil
}

func compileOldLeaf(fieldName, op string, raw any, input CompileInput) (string, []any, bool, bool, error) {
	if err := tools.ValidateIdentifier(fieldName); err != nil {
		return "", nil, false, false, err
	}

	if input.Target.DefinitionType == DefinitionTypeOrganization {
		switch value := raw.(type) {
		case string:
			if value == "auth.role" {
				return compileMembershipRolePredicate(fieldName, op, input)
			}
			if value == "auth.id" {
				return fmt.Sprintf("[%s] %s ?", fieldName, sqlOperator(op)), []any{input.Principal.UserID}, true, false, nil
			}
		}
	}

	compare := resolveValue(raw, input)
	if op == "is" || op == "is_not" {
		return fmt.Sprintf("[%s] %s NULL", fieldName, sqlOperator(op)), nil, true, false, nil
	}
	if op == "in" {
		list, ok := compare.([]any)
		if !ok {
			return "", nil, false, false, fmt.Errorf("in operator requires array value")
		}
		if len(list) == 0 {
			return "", nil, false, false, nil
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(list)), ",")
		return fmt.Sprintf("[%s] IN (%s)", fieldName, placeholders), list, true, false, nil
	}
	return fmt.Sprintf("[%s] %s ?", fieldName, sqlOperator(op)), []any{compare}, true, false, nil
}

func compileMembershipRolePredicate(fieldName, op string, input CompileInput) (string, []any, bool, bool, error) {
	operator := sqlOperator(op)
	sqlExpr := fmt.Sprintf("EXISTS (SELECT 1 FROM __ab_membership m WHERE m.role %s [%s] AND m.user_id = ?)", operator, fieldName)
	return sqlExpr, []any{input.Principal.UserID}, true, true, nil
}

func sqlOperator(op string) string {
	switch op {
	case "eq":
		return "="
	case "ne":
		return "!="
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	case "is":
		return "IS"
	case "is_not":
		return "IS NOT"
	default:
		return "="
	}
}

func resolveValue(raw any, input CompileInput) any {
	ref, ok := raw.(string)
	if !ok {
		return raw
	}
	switch ref {
	case "auth.id":
		return input.Principal.UserID
	case "auth.status":
		return string(input.Principal.AuthStatus)
	default:
		if strings.HasPrefix(ref, "new.") && input.NewValues != nil {
			return input.NewValues[strings.TrimPrefix(ref, "new.")]
		}
		return raw
	}
}

func splitScopedRef(ref string) (string, string, error) {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid policy field %q", ref)
	}
	return parts[0], parts[1], nil
}

func compareValues(left any, op string, right any) (bool, error) {
	switch op {
	case "eq":
		return fmt.Sprint(left) == fmt.Sprint(right), nil
	case "ne":
		return fmt.Sprint(left) != fmt.Sprint(right), nil
	case "is":
		return left == nil && right == nil, nil
	case "is_not":
		return left != nil, nil
	default:
		return false, nil
	}
}
