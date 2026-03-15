package auth

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/atombasedev/atombase/tools"
)

type ManagementPermission struct {
	Any   bool
	Roles []string
}

type ManagementPolicy struct {
	Invite            ManagementPermission
	AssignRole        ManagementPermission
	RemoveMember      ManagementPermission
	UpdateOrg         bool
	DeleteOrg         bool
	TransferOwnership bool
}

type ManagementMap map[string]ManagementPolicy

type OrganizationResolver interface {
	DB() *sql.DB
	LookupOrganizationTenant(ctx context.Context, organizationID string) (string, string, error)
	LookupOrganizationAuthz(ctx context.Context, organizationID string) (string, string, ManagementMap, error)
	DeleteOrganization(ctx context.Context, organizationID string) error
}

type API struct {
	db    *sql.DB
	store OrganizationResolver
}

type orgActor struct {
	Session   *Session
	UserID    string
	IsService bool
}

func NewAPI(store OrganizationResolver) *API {
	if store == nil {
		return &API{}
	}
	return &API{db: store.DB(), store: store}
}

func (api *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/magic-link/start", api.withBody(api.handleMagicLinkStart))
	mux.HandleFunc("GET /auth/magic-link/complete", api.handleMagicLinkComplete)
	mux.HandleFunc("POST /auth/signout", api.handleSignout)
	mux.HandleFunc("GET /auth/me", api.handleMe)
	mux.HandleFunc("GET /auth/orgs/{orgID}/members", api.handleListOrganizationMembers)
	mux.HandleFunc("POST /auth/orgs/{orgID}/members", api.withBody(api.handleCreateOrganizationMember))
	mux.HandleFunc("PATCH /auth/orgs/{orgID}/members/{userID}", api.withBody(api.handleUpdateOrganizationMember))
	mux.HandleFunc("DELETE /auth/orgs/{orgID}/members/{userID}", api.handleDeleteOrganizationMember)
	mux.HandleFunc("PATCH /auth/orgs/{orgID}", api.withBody(api.handleUpdateOrganization))
	mux.HandleFunc("DELETE /auth/orgs/{orgID}", api.handleDeleteOrganization)
	mux.HandleFunc("POST /auth/orgs/{orgID}/transfer-ownership", api.withBody(api.handleTransferOrganizationOwnership))
}

func (api *API) withBody(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tools.LimitBody(w, r)
		defer r.Body.Close()
		handler(w, r)
	}
}

// POST /auth/magic-link/start
func (api *API) handleMagicLinkStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	if err := BeginMagicLogin(req.Email, api.db, r.Context()); err != nil {
		if err == ErrInvalidEmail {
			tools.RespErr(w, tools.InvalidRequestErr("invalid email"))
			return
		}
		tools.RespErr(w, err)
		return
	}

	tools.RespondJSON(w, http.StatusOK, map[string]string{
		"message": "magic link sent",
	})
}

// GET /auth/magic-link/complete?token=...
func (api *API) handleMagicLinkComplete(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		tools.RespErr(w, tools.InvalidRequestErr("token is required"))
		return
	}

	user, session, isNew, err := CompleteMagicLink(token, api.db, r.Context())
	if err != nil {
		if err == ErrInvalidOrExpiredMagicLink {
			tools.RespErr(w, tools.UnauthorizedErr("invalid or expired magic link"))
			return
		}
		tools.RespErr(w, err)
		return
	}

	tools.RespondJSON(w, http.StatusOK, map[string]any{
		"user":       user,
		"token":      session.Token(),
		"expires_at": session.ExpiresAt,
		"is_new":     isNew,
	})
}

// POST /auth/signout
func (api *API) handleSignout(w http.ResponseWriter, r *http.Request) {
	session, err := api.getSession(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}

	if err := DeleteSession(session.Id, api.db, r.Context()); err != nil {
		tools.RespErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /auth/me
func (api *API) handleMe(w http.ResponseWriter, r *http.Request) {
	session, err := api.getSession(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}

	user, err := GetUserByID(session.UserID, api.db, r.Context())
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	tools.RespondJSON(w, http.StatusOK, user)
}

// Extract and validate session from auth context
func (api *API) getSession(r *http.Request) (*Session, error) {
	auth := tools.GetAuthContext(r.Context())
	if auth.Role != tools.RoleUser || auth.Token == "" {
		return nil, ErrInvalidSession
	}
	return ValidateSession(SessionToken(auth.Token), api.db, r.Context())
}

func (api *API) getOrgActor(r *http.Request) (*orgActor, error) {
	auth := tools.GetAuthContext(r.Context())
	switch auth.Role {
	case tools.RoleService:
		return &orgActor{IsService: true}, nil
	case tools.RoleUser:
		session, err := api.getSession(r)
		if err != nil {
			return nil, err
		}
		return &orgActor{
			Session: session,
			UserID:  session.UserID,
		}, nil
	default:
		return nil, ErrInvalidSession
	}
}
