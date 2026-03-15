package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/tools"
)

type CreateUserDatabaseParams struct {
	UserID     string
	Definition string
}

type createUserDatabaseRequest struct {
	Definition string `json:"definition"`
}

type UserDatabase struct {
	ID                string `json:"id"`
	DefinitionID      int32  `json:"definitionId"`
	DefinitionName    string `json:"definitionName"`
	DefinitionType    string `json:"definitionType"`
	DefinitionVersion int    `json:"definitionVersion"`
}

func (api *API) handleCreateUserDatabase(w http.ResponseWriter, r *http.Request) {
	session, err := api.getSession(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}

	var req createUserDatabaseRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	database, err := api.createUserDatabase(r.Context(), session.UserID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusCreated, database)
}

func (api *API) createUserDatabase(ctx context.Context, userID string, req createUserDatabaseRequest) (*UserDatabase, error) {
	if api == nil || api.db == nil || api.store == nil {
		return nil, tools.InvalidRequestErr("auth api not initialized")
	}

	userID = strings.TrimSpace(userID)
	req.Definition = strings.TrimSpace(req.Definition)
	if userID == "" {
		return nil, tools.UnauthorizedErr("invalid session")
	}
	if req.Definition == "" {
		return nil, tools.InvalidRequestErr("definition is required")
	}

	user, err := GetUserByID(userID, api.db, ctx)
	if err != nil {
		return nil, err
	}
	if user.DatabaseID != nil && *user.DatabaseID != "" {
		return nil, tools.ErrDatabaseExists
	}
	provisionMeta, err := api.store.LookupDefinitionProvision(ctx, req.Definition)
	if err != nil {
		return nil, err
	}
	if provisionMeta.Type != definitions.DefinitionTypeUser {
		return nil, tools.InvalidRequestErr("definition must be a user definition")
	}
	allowed, err := definitions.EvaluateProvision(&definitions.ProvisionPolicy{
		DefinitionID: provisionMeta.ID,
		Version:      provisionMeta.Version,
		Condition:    provisionMeta.Provision,
	}, definitions.ProvisionSubject{
		AuthStatus: definitions.AuthStatusAuthenticated,
		UserID:     user.ID,
		Email:      user.Email,
		Verified:   user.EmailVerifiedAt != nil,
	})
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, definitions.ProvisionDeniedErr()
	}

	return api.store.CreateUserDatabase(ctx, CreateUserDatabaseParams{
		UserID:     userID,
		Definition: req.Definition,
	})
}
