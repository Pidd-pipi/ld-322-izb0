import { Button, Card, Divider, Empty, List, Space, Typography, message } from 'antd'; import { CheckOutlined, ReloadOutlined } from '@ant-design/icons'; import { useCallback, useEffect, useMemo, useState } from 'react'; import type { Greenhouse, MeasurementBatch } from '../../types/domain'; import { addBatchEntry, createBatch, deleteBatchEntry, listBatches, submitBatch, updateBatchEntry } from '../../api/batch'; import { apiErrorData, apiErrorMessage } from '../../api/client'; import BatchStatusTag from './BatchStatusTag'; import BatchIssueAlert from './BatchIssueAlert'; import BatchEntryForm from './BatchEntryForm'; import BatchEntryTable from './BatchEntryTable';

export default function BatchPanel({ greenhouse, onArchived }: { greenhouse: Greenhouse; onArchived: () => void }) {
  const [batches, setBatches] = useState<MeasurementBatch[]>([]);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const load = useCallback(async () => {
    try { setLoading(true); setBatches(await listBatches(greenhouse.id)); } catch { message.error('批次信息加载失败'); } finally { setLoading(false); }
  }, [greenhouse.id]);
  useEffect(() => { void load(); }, [load]);
  const active = useMemo(() => batches.find((item) => item.status !== 'archived'), [batches]);
  const archived = useMemo(() => batches.filter((item) => item.status === 'archived'), [batches]);
  const replaceBatch = (batch: MeasurementBatch) => setBatches((prev) => prev.some((item) => item.id === batch.id) ? prev.map((item) => item.id === batch.id ? batch : item) : [batch, ...prev]);
  const guard = async (action: () => Promise<MeasurementBatch>, fallback: string) => {
    try { replaceBatch(await action()); return true; } catch (err) { message.error(apiErrorMessage(err, fallback)); return false; }
  };
  const create = async () => { await guard(() => createBatch(greenhouse.id), '创建批次失败'); };
  const addEntry = async (sensorId: number, value: number) => { if (active) await guard(() => addBatchEntry(active.id, sensorId, value), '录入失败'); };
  const saveEntry = async (entryId: number, value: number) => { if (active) await guard(() => updateBatchEntry(active.id, entryId, value), '修正失败'); };
  const removeEntry = async (entryId: number) => { if (active) await guard(() => deleteBatchEntry(active.id, entryId), '删除失败'); };
  const submit = async () => {
    if (!active) return;
    try {
      setSubmitting(true);
      replaceBatch(await submitBatch(active.id));
      message.success('复核通过，批次已归档并写入正式读数');
      onArchived();
    } catch (err) {
      const rejected = apiErrorData<MeasurementBatch>(err);
      if (rejected) replaceBatch(rejected);
      message.error(apiErrorMessage(err, '批次提交失败'));
    } finally { setSubmitting(false); }
  };
  const usedSensorIds = useMemo(() => new Set((active?.entries ?? []).map((entry) => entry.sensorId)), [active]);
  return <Card loading={loading} title={<Space>测量复核批次{active && <BatchStatusTag status={active.status} />}</Space>} extra={<Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button>}>
    {!active && <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Empty description="当前没有待复核的测量批次" />
      <Button type="primary" block onClick={create}>创建测量批次</Button>
    </Space>}
    {active && <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <BatchIssueAlert issues={active.issues} />
      <Typography.Text type="secondary">批次 #{active.id} · 已录入 {active.entries?.length ?? 0} / {greenhouse.sensors?.length ?? 0} 个传感器 · 同一传感器仅保留一条，重复录入将被拒绝</Typography.Text>
      <BatchEntryForm sensors={greenhouse.sensors ?? []} usedSensorIds={usedSensorIds} onAdd={addEntry} />
      <BatchEntryTable entries={active.entries ?? []} issues={active.issues} editable onSave={saveEntry} onDelete={removeEntry} />
      <Button type="primary" icon={<CheckOutlined />} loading={submitting} disabled={!active.entries?.length} onClick={submit}>提交复核</Button>
    </Space>}
    {archived.length > 0 && <><Divider plain>历史归档批次</Divider>
      <List size="small" dataSource={archived} renderItem={(item) => <List.Item><Space><BatchStatusTag status={item.status} /><span>批次 #{item.id}</span><Typography.Text type="secondary">{item.entries?.length ?? 0} 条读数 · 归档于 {item.archivedAt ? new Date(item.archivedAt).toLocaleString() : '-'}</Typography.Text></Space></List.Item>} />
    </>}
  </Card>;
}
