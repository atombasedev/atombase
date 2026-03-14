package data

import (
	"context"
	"errors"
)

func (dao *TenantConnection) updateSchema() error {
	cols, err := SchemaCols(dao.Client)
	if err != nil {
		return err
	}
	fks, err := schemaFks(dao.Client)
	if err != nil {
		return err
	}
	ftsTables, err := schemaFTS(dao.Client)
	if err != nil {
		return err
	}

	dao.Schema = SchemaCache{Tables: cols, Fks: fks, FTSTables: ftsTables}

	return nil
}

func (dao *TenantConnection) InvalidateSchema(_ context.Context) error {
	if dao.primaryStore == nil || dao.primaryStore.DB() == nil {
		return errors.New("primary store not initialized")
	}

	// Database instances: reload from template cache.
	schema, version, err := GetCachedTemplate(dao.primaryStore.DB(), dao.TemplateID)
	if err != nil {
		return err
	}
	dao.Schema = schema
	dao.SchemaVersion = version
	return nil
}
