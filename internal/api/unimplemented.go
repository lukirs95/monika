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
func (unimplemented) ListFindingHistory(context.Context, oapi.ListFindingHistoryRequestObject) (oapi.ListFindingHistoryResponseObject, error) {
	return nil, errNotImplemented
}
func (unimplemented) ListRunHistory(context.Context, oapi.ListRunHistoryRequestObject) (oapi.ListRunHistoryResponseObject, error) {
	return nil, errNotImplemented
}
