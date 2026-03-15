package auth

import "github.com/atombasedev/atombase/definitions"

type DefinitionProvisionMeta struct {
	ID        int32
	Name      string
	Type      definitions.DefinitionType
	Version   int
	Provision *definitions.Condition
}
