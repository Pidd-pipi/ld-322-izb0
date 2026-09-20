package constants

// 测量复核批次状态机：pending → (rejected →) archived
const (
	BatchPending  = "pending"
	BatchRejected = "rejected"
	BatchArchived = "archived"
)

// 批次校验问题类型
const (
	IssueMissing    = "missing"
	IssueDuplicate  = "duplicate"
	IssueOutOfRange = "out_of_range"
	IssueForeign    = "foreign_sensor"
)

const EventBatch = "batch.updated"
