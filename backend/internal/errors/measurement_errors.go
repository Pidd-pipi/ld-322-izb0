package apperrors

import "net/http"

// 测量复核闭环的业务错误，错误码集中管理。
var (
	// ErrBatchNotFound 复核批次不存在。
	ErrBatchNotFound = New(40404, "复核批次不存在", http.StatusNotFound)
	// ErrBatchClosed 批次已归档，禁止再录入或重复提交。
	ErrBatchClosed = New(40901, "批次已归档，禁止追加录入或重复提交", http.StatusConflict)
	// ErrEntryConflict 同一传感器在该批次已有一条录入，重复/并发录入不得覆盖。
	ErrEntryConflict = New(40902, "该传感器在本批次已有录入，请勿重复提交", http.StatusConflict)
	// ErrSensorNotInGreenhouse 只允许录入批次所属温室配置的传感器。
	ErrSensorNotInGreenhouse = New(40301, "只允许录入该温室配置的传感器", http.StatusForbidden)
	// ErrBatchNoConflict 并发创建导致批次号撞号，调用方应重新取号重试。
	ErrBatchNoConflict = New(40903, "批次编号冲突，请重试", http.StatusConflict)
)
