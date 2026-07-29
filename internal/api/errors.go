package api

import "go.lumeweb.com/portal/core"

const (
	Namespace              = "meta"
	ErrKeyCIDNotFound      core.ErrorType = "CID_NOT_FOUND"
	ErrKeyObjectNotReady   core.ErrorType = "OBJECT_NOT_READY"
	ErrKeyInvalidCID       core.ErrorType = "INVALID_CID"
	ErrKeyFetchFailed      core.ErrorType = "FETCH_FAILED"
	ErrKeyExportFailed     core.ErrorType = "EXPORT_FAILED"
	ErrKeyExportDenied     core.ErrorType = "EXPORT_DENIED"
	ErrKeyDAGNotSupported  core.ErrorType = "DAG_NOT_SUPPORTED"
)

func init() {
	core.MustRegisterNamespace(Namespace)
	core.MustRegisterDefaultErrorMessages(Namespace, map[core.ErrorType]core.ErrorDefinition{
		ErrKeyCIDNotFound:      {Key: ErrKeyCIDNotFound, Message: "CID not found"},
		ErrKeyObjectNotReady:   {Key: ErrKeyObjectNotReady, Message: "Object is staged/packing; try again later"},
		ErrKeyInvalidCID:       {Key: ErrKeyInvalidCID, Message: "Invalid CID format"},
		ErrKeyFetchFailed:      {Key: ErrKeyFetchFailed, Message: "Failed to fetch stats"},
		ErrKeyExportFailed:     {Key: ErrKeyExportFailed, Message: "Failed to export object"},
		ErrKeyExportDenied:     {Key: ErrKeyExportDenied, Message: "Protocol denied export for this CID"},
		ErrKeyDAGNotSupported:  {Key: ErrKeyDAGNotSupported, Message: "Protocol does not support DAG traversal"},
	})
}
