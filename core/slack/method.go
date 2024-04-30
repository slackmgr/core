//nolint:revive,stylecheck
package slack

type Method string

var (
	POST           = Method("POST")
	UPDATE         = Method("UPDATE")
	UPDATE_DELETED = Method("UPDATE_DELETED")
)
