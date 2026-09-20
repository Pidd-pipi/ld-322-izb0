package apperrors

import "net/http"

type BusinessError struct {
	Code    int
	Message string
	Status  int
}

func (e *BusinessError) Error() string { return e.Message }
func New(code int, message string, status int) *BusinessError {
	return &BusinessError{Code: code, Message: message, Status: status}
}

var (
	ErrNotFound       = New(40401, "资源不存在", http.StatusNotFound)
	ErrValidation     = New(40001, "请求参数不合法", http.StatusBadRequest)
	ErrUnauthorized   = New(40101, "认证失败", http.StatusUnauthorized)
	ErrInternal       = New(50001, "服务器内部错误", http.StatusInternalServerError)
	ErrSensorMismatch = New(40002, "传感器不属于该温室，禁止录入本批次", http.StatusBadRequest)
	ErrBatchOpen      = New(40901, "当前温室存在未归档的测量批次，请先完成复核", http.StatusConflict)
	ErrEntryDuplicate = New(40902, "该传感器在本批次中已录入，禁止覆盖", http.StatusConflict)
	ErrBatchArchived  = New(40903, "批次已归档，禁止追加或重复提交", http.StatusConflict)
	ErrBatchRejected  = New(42201, "批次校验未通过，请修正后重新提交", http.StatusUnprocessableEntity)
)
