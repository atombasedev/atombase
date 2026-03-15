package definitions

import "testing"

func TestCompiler_OrganizationAuthRoleCompilesToMembershipPredicate(t *testing.T) {
	compiler := NewCompiler()
	policy := &AccessPolicy{
		Condition: &Condition{Field: "auth.role", Op: "eq", Value: "owner"},
	}

	predicate, err := compiler.Compile(policy, CompileInput{
		Principal: Principal{UserID: "user-1", AuthStatus: AuthStatusAuthenticated},
		Target: DatabaseTarget{
			DatabaseID:        "org-db",
			DefinitionID:      1,
			DefinitionType:    DefinitionTypeOrganization,
			DefinitionVersion: 1,
		},
		Table:     "projects",
		Operation: "select",
	})
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if predicate.SQL == "" {
		t.Fatal("expected SQL predicate for organization role policy")
	}
	if len(predicate.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(predicate.Args))
	}
}

func TestValidateConditionContext_RejectsInvalidScopes(t *testing.T) {
	err := ValidateConditionContext(Condition{Field: "auth.role", Op: "eq", Value: "owner"}, "select", DefinitionTypeGlobal)
	if err == nil {
		t.Fatal("expected auth.role on global definition to fail validation")
	}

	err = ValidateConditionContext(Condition{Field: "new.status", Op: "eq", Value: "draft"}, "delete", DefinitionTypeOrganization)
	if err == nil {
		t.Fatal("expected new.* in delete policy to fail validation")
	}
}

func TestValidateProvisionCondition_RejectsInvalidFields(t *testing.T) {
	if _, err := ParseAndValidateProvision(DefinitionTypeGlobal, &Condition{
		Field: "auth.verified",
		Op:    "eq",
		Value: true,
	}); err == nil {
		t.Fatal("expected global provision policy to fail")
	}

	if _, err := ParseAndValidateProvision(DefinitionTypeUser, &Condition{
		Field: "old.id",
		Op:    "eq",
		Value: 1,
	}); err == nil {
		t.Fatal("expected old.* in provision policy to fail")
	}
}

func TestEvaluateProvision_UsesPrimaryAuthContext(t *testing.T) {
	allowed, err := EvaluateProvision(&ProvisionPolicy{
		Condition: &Condition{Field: "auth.verified", Op: "eq", Value: true},
	}, ProvisionSubject{
		AuthStatus: AuthStatusAuthenticated,
		UserID:     "user-1",
		Email:      "user@example.com",
		Verified:   true,
	})
	if err != nil {
		t.Fatalf("EvaluateProvision failed: %v", err)
	}
	if !allowed {
		t.Fatal("expected verified user to satisfy provision policy")
	}

	allowed, err = EvaluateProvision(&ProvisionPolicy{
		Condition: &Condition{Field: "auth.email", Op: "eq", Value: "allowed@example.com"},
	}, ProvisionSubject{
		AuthStatus: AuthStatusAuthenticated,
		UserID:     "user-1",
		Email:      "blocked@example.com",
		Verified:   true,
	})
	if err != nil {
		t.Fatalf("EvaluateProvision failed: %v", err)
	}
	if allowed {
		t.Fatal("expected mismatched email to deny provisioning")
	}

	allowed, err = EvaluateProvision(nil, ProvisionSubject{IsService: true})
	if err != nil {
		t.Fatalf("EvaluateProvision service bypass failed: %v", err)
	}
	if !allowed {
		t.Fatal("expected service subject to bypass provisioning rules")
	}
}
