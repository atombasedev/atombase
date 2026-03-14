package data

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atombasedev/atombase/tools"
)

// Batch executes multiple operations atomically within a tenant database transaction.
func (dao *TenantConnection) Batch(ctx context.Context, req BatchRequest) (BatchResponse, error) {
	if len(req.Operations) == 0 {
		return BatchResponse{Results: []any{}}, nil
	}

	if len(req.Operations) > MaxBatchOperations {
		return BatchResponse{}, tools.ErrBatchTooLarge
	}

	tx, err := dao.Client.BeginTx(ctx, nil)
	if err != nil {
		return BatchResponse{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	results := make([]any, len(req.Operations))

	for i, op := range req.Operations {
		result, err := dao.executeOperation(ctx, tx, op)
		if err != nil {
			return BatchResponse{}, fmt.Errorf("operation %d (%s on %s): %w", i, op.Operation, op.Table, err)
		}
		results[i] = result
	}

	if err := tx.Commit(); err != nil {
		return BatchResponse{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return BatchResponse{Results: results}, nil
}

// executeOperation executes a single batch operation within a transaction.
func (dao *TenantConnection) executeOperation(ctx context.Context, tx Executor, op BatchOperation) (any, error) {
	switch op.Operation {
	case "select":
		var query SelectQuery
		if err := mapToStruct(op.Body, &query); err != nil {
			return nil, err
		}
		result, err := dao.selectJSON(ctx, tx, op.Table, query, op.Count)
		if err != nil {
			return nil, err
		}
		var data []any
		if err := json.Unmarshal(result.Data, &data); err != nil {
			return nil, err
		}
		// If count was requested, return an object with data and count
		if op.Count {
			return map[string]any{
				"data":  data,
				"count": result.Count,
			}, nil
		}
		return data, nil

	case "insert":
		var req InsertRequest
		if err := mapToStruct(op.Body, &req); err != nil {
			return nil, err
		}
		data, err := dao.insertJSON(ctx, tx, op.Table, req)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil

	case "upsert":
		var req UpsertRequest
		if err := mapToStruct(op.Body, &req); err != nil {
			return nil, err
		}
		data, err := dao.upsertJSON(ctx, tx, op.Table, req)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil

	case "update":
		var req UpdateRequest
		if err := mapToStruct(op.Body, &req); err != nil {
			return nil, err
		}
		data, err := dao.updateJSON(ctx, tx, op.Table, req)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil

	case "delete":
		var req DeleteRequest
		if err := mapToStruct(op.Body, &req); err != nil {
			return nil, err
		}
		data, err := dao.deleteJSON(ctx, tx, op.Table, req)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unknown operation: %s", op.Operation)
	}
}

// mapToStruct converts a map[string]any to a struct using JSON marshaling.
func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
