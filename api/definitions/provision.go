package definitions

import (
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/tools"
)

type ProvisionSubject struct {
	AuthStatus AuthStatus
	UserID     string
	Email      string
	Verified   bool
	IsService  bool
}

func EvaluateProvision(policy *ProvisionPolicy, subject ProvisionSubject) (bool, error) {
	if subject.IsService {
		return true, nil
	}
	if policy == nil || policy.Condition == nil || policy.Condition.IsZero() {
		return false, nil
	}
	return evalProvisionCondition(*policy.Condition, subject)
}

func evalProvisionCondition(cond Condition, subject ProvisionSubject) (bool, error) {
	switch {
	case cond.Field != "":
		return evalProvisionLeaf(cond, subject)
	case len(cond.And) > 0:
		for _, child := range cond.And {
			ok, err := evalProvisionCondition(child, subject)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case len(cond.Or) > 0:
		for _, child := range cond.Or {
			ok, err := evalProvisionCondition(child, subject)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case cond.Not != nil:
		ok, err := evalProvisionCondition(*cond.Not, subject)
		if err != nil {
			return false, err
		}
		return !ok, nil
	default:
		return false, nil
	}
}

func evalProvisionLeaf(cond Condition, subject ProvisionSubject) (bool, error) {
	scope, fieldName, err := splitScopedRef(cond.Field)
	if err != nil {
		return false, err
	}
	if scope != "auth" {
		return false, fmt.Errorf("unsupported provision policy scope %q", scope)
	}

	var left any
	switch fieldName {
	case "status":
		left = string(subject.AuthStatus)
	case "id":
		left = subject.UserID
	case "email":
		left = subject.Email
	case "verified":
		left = subject.Verified
	default:
		return false, fmt.Errorf("unsupported provision auth field %q", fieldName)
	}

	right := resolveProvisionValue(cond.Value, subject)
	if cond.Op == "in" {
		list, ok := right.([]any)
		if !ok {
			return false, fmt.Errorf("in operator requires array value")
		}
		for _, item := range list {
			ok, err := compareValues(left, "eq", item)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if cond.Op == "is" || cond.Op == "is_not" {
		return compareValues(left, cond.Op, right)
	}
	ok, err := compareValues(left, cond.Op, right)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func resolveProvisionValue(raw any, subject ProvisionSubject) any {
	ref, ok := raw.(string)
	if !ok {
		return raw
	}
	switch ref {
	case "auth.status":
		return string(subject.AuthStatus)
	case "auth.id":
		return subject.UserID
	case "auth.email":
		return subject.Email
	case "auth.verified":
		return subject.Verified
	default:
		return raw
	}
}

func LoadProvisionPolicyFromJSON(raw string, definitionID int32, version int) (*ProvisionPolicy, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	cond, err := DecodeCondition(raw)
	if err != nil {
		return nil, err
	}
	if cond == nil {
		return nil, nil
	}
	return &ProvisionPolicy{
		DefinitionID: definitionID,
		Version:      version,
		Condition:    cond,
	}, nil
}

func ProvisionDeniedErr() error {
	return tools.UnauthorizedErr("definition provisioning is not allowed")
}
