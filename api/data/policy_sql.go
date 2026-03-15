package data

import "github.com/atombasedev/atombase/definitions"

func appendPolicyWhere(where string, args []any, policy definitions.CompiledPredicate) (string, []any) {
	if policy.SQL == "" {
		return where, args
	}
	if where == "" {
		return "WHERE " + policy.SQL + " ", append(args, policy.Args...)
	}
	return where + "AND " + policy.SQL + " ", append(args, policy.Args...)
}

func applyPolicyCTE(query string, args []any, dao *TenantConnection, needsMembership bool) (string, []any) {
	if !needsMembership || dao.DefinitionType != "organization" {
		return query, args
	}
	query = "WITH __ab_membership AS (SELECT user_id, role, status FROM atombase_membership WHERE user_id = ?) " + query
	return query, append([]any{dao.Principal.UserID}, args...)
}
