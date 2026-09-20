import { Tag } from 'antd'; import type { BatchStatus } from '../../types/domain';
const meta: Record<BatchStatus, { color: string; text: string }> = { pending: { color: 'processing', text: '待提交' }, rejected: { color: 'error', text: '校验失败' }, archived: { color: 'success', text: '已归档' } };
export default function BatchStatusTag({ status }: { status: BatchStatus }) { const item = meta[status] ?? { color: 'default', text: status }; return <Tag color={item.color}>{item.text}</Tag>; }
