package data

import (
	"context"

	"github.com/atombasedev/atombase/definitions"
)

func (dao *TenantConnection) compilePolicy(ctx context.Context, table, operation string, values map[string]any) (definitions.CompiledPredicate, error) {
	return dao.compilePolicyWithInput(ctx, table, operation, values, "")
}

func (dao *TenantConnection) compilePolicyWithNewAlias(ctx context.Context, table, operation string, alias string) (definitions.CompiledPredicate, error) {
	return dao.compilePolicyWithInput(ctx, table, operation, nil, alias)
}

func (dao *TenantConnection) compilePolicyWithInput(ctx context.Context, table, operation string, values map[string]any, newAlias string) (definitions.CompiledPredicate, error) {
	if dao == nil || dao.primaryStore == nil || dao.DefinitionID == 0 {
		return definitions.CompiledPredicate{GoAllowed: true}, nil
	}
	policy, err := dao.primaryStore.LoadAccessPolicy(ctx, dao.DefinitionID, dao.DatabaseVersion, table, operation)
	if err != nil {
		return definitions.CompiledPredicate{}, err
	}

	return definitions.NewCompiler().Compile(policy, definitions.CompileInput{
		Principal: dao.Principal,
		Target: definitions.DatabaseTarget{
			DatabaseID:        dao.ID,
			DefinitionID:      dao.DefinitionID,
			DefinitionType:    dao.DefinitionType,
			DefinitionVersion: dao.DatabaseVersion,
		},
		Table:     table,
		Operation: operation,
		NewValues: values,
		NewAlias:  newAlias,
	})
}
