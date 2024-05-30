package slack

type UpdateMethod string

const (
	UpdateMethodPost          = UpdateMethod("POST")
	UpdateMethodUpdate        = UpdateMethod("UPDATE")
	UpdateMethodUpdateDeleted = UpdateMethod("UPDATE_DELETED")
)
