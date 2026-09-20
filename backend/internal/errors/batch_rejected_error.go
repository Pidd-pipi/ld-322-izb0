package apperrors

import (
	"net/http"

	"github.com/cygreenenv/greenhouse-panel/internal/model"
)

// BatchRejectedError 表示提交复核时整批被拒绝：缺项、重复或超阈值。
// 不生成任何正式读数，携带待修正项返回给调用方。
type BatchRejectedError struct {
	BatchID uint
	Issues  model.BatchIssues
}

func (e *BatchRejectedError) Error() string { return "批次校验未通过，整批拒绝" }

// Status 提交语义本身成立但业务校验失败，返回 422。
func (e *BatchRejectedError) Status() int { return http.StatusUnprocessableEntity }
