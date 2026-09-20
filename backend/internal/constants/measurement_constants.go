package constants

// 测量复核批次相关的事件名与批次号格式。
const (
	EventBatchEntryCreated = "batch.entry_created"
	EventBatchSubmitted    = "batch.submitted"

	BatchNoPrefix     = "MB"
	BatchNoTimeLayout = "20060102"
)
