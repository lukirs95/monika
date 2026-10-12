package api

import (
	"context"

	"github.com/lukirs95/monika/internal/api/oapi"
)

// unimplemented beantwortet alle Operationen, die noch fehlen, mit 501. Server bettet es ein; jede
// fertige Methode von Server überdeckt die hiesige. Entfällt, wenn die API vollständig ist.
type unimplemented struct{}

func (unimplemented) ListAudit(context.Context, oapi.ListAuditRequestObject) (oapi.ListAuditResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetEvents(context.Context, oapi.GetEventsRequestObject) (oapi.GetEventsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) Watch(context.Context, oapi.WatchRequestObject) (oapi.WatchResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListFindingHistory(context.Context, oapi.ListFindingHistoryRequestObject) (oapi.ListFindingHistoryResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListForwardTargets(context.Context, oapi.ListForwardTargetsRequestObject) (oapi.ListForwardTargetsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateForwardTarget(context.Context, oapi.CreateForwardTargetRequestObject) (oapi.CreateForwardTargetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteForwardTarget(context.Context, oapi.DeleteForwardTargetRequestObject) (oapi.DeleteForwardTargetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateForwardTarget(context.Context, oapi.UpdateForwardTargetRequestObject) (oapi.UpdateForwardTargetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListRoles(context.Context, oapi.ListRolesRequestObject) (oapi.ListRolesResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateRole(context.Context, oapi.CreateRoleRequestObject) (oapi.CreateRoleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteRole(context.Context, oapi.DeleteRoleRequestObject) (oapi.DeleteRoleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateRole(context.Context, oapi.UpdateRoleRequestObject) (oapi.UpdateRoleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) SetRoleGrants(context.Context, oapi.SetRoleGrantsRequestObject) (oapi.SetRoleGrantsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) SetRoleMembers(context.Context, oapi.SetRoleMembersRequestObject) (oapi.SetRoleMembersResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListRunHistory(context.Context, oapi.ListRunHistoryRequestObject) (oapi.ListRunHistoryResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListSecrets(context.Context, oapi.ListSecretsRequestObject) (oapi.ListSecretsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateSecret(context.Context, oapi.CreateSecretRequestObject) (oapi.CreateSecretResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteSecret(context.Context, oapi.DeleteSecretRequestObject) (oapi.DeleteSecretResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ReplaceSecretValue(context.Context, oapi.ReplaceSecretValueRequestObject) (oapi.ReplaceSecretValueResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) RevokeToken(context.Context, oapi.RevokeTokenRequestObject) (oapi.RevokeTokenResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListUsers(context.Context, oapi.ListUsersRequestObject) (oapi.ListUsersResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateUser(context.Context, oapi.CreateUserRequestObject) (oapi.CreateUserResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateUser(context.Context, oapi.UpdateUserRequestObject) (oapi.UpdateUserResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) SetUserPassword(context.Context, oapi.SetUserPasswordRequestObject) (oapi.SetUserPasswordResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListTokens(context.Context, oapi.ListTokensRequestObject) (oapi.ListTokensResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateToken(context.Context, oapi.CreateTokenRequestObject) (oapi.CreateTokenResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ResetUserTOTP(context.Context, oapi.ResetUserTOTPRequestObject) (oapi.ResetUserTOTPResponseObject, error) {
	return nil, errNotImplemented
}
