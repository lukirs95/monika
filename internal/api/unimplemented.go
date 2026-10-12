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
func (unimplemented) KioskLogin(context.Context, oapi.KioskLoginRequestObject) (oapi.KioskLoginResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) Login(context.Context, oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) LoginTOTP(context.Context, oapi.LoginTOTPRequestObject) (oapi.LoginTOTPResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) Logout(context.Context, oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetMe(context.Context, oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ChangeOwnPassword(context.Context, oapi.ChangeOwnPasswordRequestObject) (oapi.ChangeOwnPasswordResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListOwnSessions(context.Context, oapi.ListOwnSessionsRequestObject) (oapi.ListOwnSessionsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) RevokeOwnSession(context.Context, oapi.RevokeOwnSessionRequestObject) (oapi.RevokeOwnSessionResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ConfirmTOTP(context.Context, oapi.ConfirmTOTPRequestObject) (oapi.ConfirmTOTPResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) EnrollTOTP(context.Context, oapi.EnrollTOTPRequestObject) (oapi.EnrollTOTPResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateDevice(context.Context, oapi.CreateDeviceRequestObject) (oapi.CreateDeviceResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteDevice(context.Context, oapi.DeleteDeviceRequestObject) (oapi.DeleteDeviceResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListSources(context.Context, oapi.ListSourcesRequestObject) (oapi.ListSourcesResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateSource(context.Context, oapi.CreateSourceRequestObject) (oapi.CreateSourceResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListDrivers(context.Context, oapi.ListDriversRequestObject) (oapi.ListDriversResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetElementFacts(context.Context, oapi.GetElementFactsRequestObject) (oapi.GetElementFactsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetElementRules(context.Context, oapi.GetElementRulesRequestObject) (oapi.GetElementRulesResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateUserFields(context.Context, oapi.UpdateUserFieldsRequestObject) (oapi.UpdateUserFieldsResponseObject, error) {
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
func (unimplemented) AckFinding(context.Context, oapi.AckFindingRequestObject) (oapi.AckFindingResponseObject, error) {
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
func (unimplemented) CreateGroup(context.Context, oapi.CreateGroupRequestObject) (oapi.CreateGroupResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteGroup(context.Context, oapi.DeleteGroupRequestObject) (oapi.DeleteGroupResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) AddGroupMember(context.Context, oapi.AddGroupMemberRequestObject) (oapi.AddGroupMemberResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetHealthz(context.Context, oapi.GetHealthzRequestObject) (oapi.GetHealthzResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) RemoveGroupMember(context.Context, oapi.RemoveGroupMemberRequestObject) (oapi.RemoveGroupMemberResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateGroupMember(context.Context, oapi.UpdateGroupMemberRequestObject) (oapi.UpdateGroupMemberResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListPresets(context.Context, oapi.ListPresetsRequestObject) (oapi.ListPresetsResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreatePreset(context.Context, oapi.CreatePresetRequestObject) (oapi.CreatePresetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeletePreset(context.Context, oapi.DeletePresetRequestObject) (oapi.DeletePresetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetPreset(context.Context, oapi.GetPresetRequestObject) (oapi.GetPresetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdatePreset(context.Context, oapi.UpdatePresetRequestObject) (oapi.UpdatePresetResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) RunPreset(context.Context, oapi.RunPresetRequestObject) (oapi.RunPresetResponseObject, error) {
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
func (unimplemented) ListRules(context.Context, oapi.ListRulesRequestObject) (oapi.ListRulesResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CreateRule(context.Context, oapi.CreateRuleRequestObject) (oapi.CreateRuleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) DeleteRule(context.Context, oapi.DeleteRuleRequestObject) (oapi.DeleteRuleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateRule(context.Context, oapi.UpdateRuleRequestObject) (oapi.UpdateRuleResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) SubmitRun(context.Context, oapi.SubmitRunRequestObject) (oapi.SubmitRunResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListRunHistory(context.Context, oapi.ListRunHistoryRequestObject) (oapi.ListRunHistoryResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetRun(context.Context, oapi.GetRunRequestObject) (oapi.GetRunResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) CancelRun(context.Context, oapi.CancelRunRequestObject) (oapi.CancelRunResponseObject, error) {
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
func (unimplemented) DeleteSource(context.Context, oapi.DeleteSourceRequestObject) (oapi.DeleteSourceResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) UpdateSource(context.Context, oapi.UpdateSourceRequestObject) (oapi.UpdateSourceResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) GetState(context.Context, oapi.GetStateRequestObject) (oapi.GetStateResponseObject, error) {
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
