import { Button, InputNumber, Popconfirm, Space, Table, Tag, Typography } from 'antd'; import { useState } from 'react'; import type { BatchIssue, MeasurementEntry } from '../../types/domain';

function EntryValueCell({ entry, editable, onSave }: { entry: MeasurementEntry; editable: boolean; onSave: (entryId: number, value: number) => Promise<void> }) {
  const [value, setValue] = useState<number>(entry.value);
  const [saving, setSaving] = useState(false);
  if (!editable) return <Typography.Text>{entry.value}</Typography.Text>;
  const save = async () => { try { setSaving(true); await onSave(entry.id, value); } finally { setSaving(false); } };
  return <Space size={4}>
    <InputNumber size="small" value={value} onChange={(next) => setValue(next ?? entry.value)} />
    <Button size="small" loading={saving} disabled={value === entry.value} onClick={save}>修正</Button>
  </Space>;
}

export default function BatchEntryTable({ entries, issues, editable, onSave, onDelete }: { entries: MeasurementEntry[]; issues?: BatchIssue[]; editable: boolean; onSave: (entryId: number, value: number) => Promise<void>; onDelete: (entryId: number) => Promise<void> }) {
  const issueBySensor = new Map((issues ?? []).map((issue) => [issue.sensorId, issue]));
  const columns = [
    { title: '传感器', key: 'sensor', render: (_: unknown, row: MeasurementEntry) => <Space size={4}><span>{row.sensor?.name ?? `#${row.sensorId}`}</span><Typography.Text type="secondary">{row.sensor?.unit}</Typography.Text></Space> },
    { title: '阈值区间', key: 'threshold', render: (_: unknown, row: MeasurementEntry) => row.sensor?.threshold ? `${row.sensor.threshold.minValue} ~ ${row.sensor.threshold.maxValue}` : '-' },
    { title: '录入值', key: 'value', render: (_: unknown, row: MeasurementEntry) => <EntryValueCell entry={row} editable={editable} onSave={onSave} /> },
    { title: '复核状态', key: 'state', render: (_: unknown, row: MeasurementEntry) => {
      const issue = issueBySensor.get(row.sensorId);
      if (issue) return <Tag color="error">{issue.message}</Tag>;
      const threshold = row.sensor?.threshold;
      const out = threshold && (row.value < threshold.minValue || row.value > threshold.maxValue);
      return out ? <Tag color="warning">越限待修正</Tag> : <Tag color="success">正常</Tag>;
    } },
  ];
  if (editable) columns.push({ title: '操作', key: 'actions', render: (_: unknown, row: MeasurementEntry) => <Popconfirm title="删除该条录入？" onConfirm={() => void onDelete(row.id)}><Button size="small" danger>删除</Button></Popconfirm> } as never);
  return <Table rowKey="id" size="small" pagination={false} dataSource={entries} columns={columns} rowClassName={(row) => issueBySensor.has(row.sensorId) ? 'batch-row-issue' : ''} locale={{ emptyText: '尚未录入任何传感器读数' }} />;
}
