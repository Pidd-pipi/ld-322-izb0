import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Descriptions, InputNumber, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { LockOutlined, SendOutlined } from '@ant-design/icons';
import type { BatchIssue, Greenhouse, MeasurementBatch, Sensor } from '../../types/domain';
import { addEntry, asBatchRejected, correctEntry, errorMessage, submitBatch } from '../../api/measurement';

const { Text } = Typography;

interface Props {
  batch: MeasurementBatch;
  greenhouse: Greenhouse;
  token: number; // 每次详情刷新自增，触发组件重载录入中的输入
  onChanged: () => void;
}

const issueColor: Record<string, string> = { missing: 'warning', duplicate: 'error', threshold: 'error' };
const issueLabel: Record<string, string> = { missing: '缺项', duplicate: '重复', threshold: '超阈值' };

export default function BatchWorkspace({ batch, greenhouse, token, onChanged }: Props) {
  const archived = batch.status === 'archived';
  const entryBySensor = useMemo(() => {
    const map = new Map<number, number>();
    (batch.entries ?? []).forEach((entry) => map.set(entry.sensorId, entry.value));
    return map;
  }, [batch.entries]);
  const [drafts, setDrafts] = useState<Record<number, number | null>>({});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => setDrafts({}), [batch.id, token]);

  const enterValue = async (sensor: Sensor) => {
    const value = drafts[sensor.id];
    if (value === null || value === undefined || Number.isNaN(value)) {
      message.warning('请先填写测量值');
      return;
    }
    try {
      await addEntry(batch.id, sensor.id, value);
      message.success(`${sensor.name} 已录入`);
      onChanged();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const fixValue = async (sensor: Sensor) => {
    const value = drafts[sensor.id];
    if (value === null || value === undefined || Number.isNaN(value)) {
      message.warning('请填写修正后的测量值');
      return;
    }
    try {
      await correctEntry(batch.id, sensor.id, value);
      message.success(`${sensor.name} 已修正`);
      onChanged();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const submit = async () => {
    try {
      const result = await submitBatch(batch.id);
      message.success(`批次已归档，写入 ${result.readings.length} 条正式读数`);
      onChanged();
    } catch (error) {
      const rejected = asBatchRejected(error);
      if (rejected) {
        message.error(`整批被拒绝，共 ${rejected.data.issues.length} 个待修正项`);
        onChanged();
        return;
      }
      message.error(errorMessage(error));
    }
  };

  const columns: ColumnsType<Sensor> = [
    { title: '传感器', dataIndex: 'name', render: (_, row) => <Text strong>{row.name}</Text> },
    {
      title: '阈值范围',
      render: (_, row) => (
        <Text type="secondary">
          {row.threshold ? `[${row.threshold.minValue}, ${row.threshold.maxValue}] ${row.unit}` : '未配置阈值'}
        </Text>
      ),
    },
    {
      title: '当前录入',
      render: (_, row) => {
        const entered = entryBySensor.has(row.id);
        if (!entered) {
          return <Tag color="warning">缺项待录</Tag>;
        }
        const value = entryBySensor.get(row.id)!;
        const out = row.threshold && (value < row.threshold.minValue || value > row.threshold.maxValue);
        return (
          <Text strong type={out ? 'danger' : undefined}>
            {value} {row.unit} {out && <Tag color="error">超阈值</Tag>}
          </Text>
        );
      },
    },
    {
      title: archived ? '归档值' : '录入 / 修正',
      render: (_, row) => {
        if (archived) {
          return <Text type="secondary"><LockOutlined /> 已锁定</Text>;
        }
        const entered = entryBySensor.has(row.id);
        return (
          <Space>
            <InputNumber
              aria-label={`${row.name}测量值`}
              style={{ width: 130 }}
              placeholder={`单位 ${row.unit}`}
              value={drafts[row.id] ?? entryBySensor.get(row.id) ?? null}
              onChange={(value) => setDrafts((prev) => ({ ...prev, [row.id]: value }))}
            />
            {entered ? (
              <Button size="small" onClick={() => void fixValue(row)}>修正</Button>
            ) : (
              <Button size="small" type="primary" ghost onClick={() => void enterValue(row)}>录入</Button>
            )}
          </Space>
        );
      },
    },
  ];

  const issues: BatchIssue[] = batch.lastIssues ?? [];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Descriptions bordered size="small" column={3}>
        <Descriptions.Item label="批次号">{batch.batchNo}</Descriptions.Item>
        <Descriptions.Item label="温室">{greenhouse.name}</Descriptions.Item>
        <Descriptions.Item label="状态">
          {archived ? <Tag color="success">已归档</Tag> : <Tag color="processing">待提交</Tag>}
        </Descriptions.Item>
        <Descriptions.Item label="录入进度">
          {batch.entries?.length ?? 0} / {greenhouse.sensors.length}
        </Descriptions.Item>
        <Descriptions.Item label="创建时间">{new Date(batch.createdAt).toLocaleString()}</Descriptions.Item>
        <Descriptions.Item label="归档时间">
          {batch.submittedAt ? new Date(batch.submittedAt).toLocaleString() : '—'}
        </Descriptions.Item>
      </Descriptions>

      {issues.length > 0 && (
        <Alert
          type="error"
          showIcon
          message={`整批校验未通过（${issues.length} 项待修正，尚未生成正式读数）`}
          description={
            <ul style={{ marginBottom: 0, paddingLeft: 18 }}>
              {issues.map((issue, index) => (
                <li key={`${issue.sensorId}-${index}`}>
                  <Tag color={issueColor[issue.code] ?? 'default'}>{issueLabel[issue.code] ?? issue.code}</Tag>
                  {issue.message}
                </li>
              ))}
            </ul>
          }
        />
      )}
      {archived && (
        <Alert
          type="success"
          showIcon
          message="批次已归档，正式读数已写入；追加录入与重复提交均会被拒绝。"
        />
      )}

      <Table<Sensor>
        rowKey="id"
        size="middle"
        pagination={false}
        columns={columns}
        dataSource={greenhouse.sensors}
      />

      {!archived && (
        <Space>
          <Button type="primary" size="large" icon={<SendOutlined />} onClick={() => void submit()}>
            提交整批复核
          </Button>
          <Text type="secondary">缺项、重复或超出阈值将整批拒绝并返回待修正项，不生成正式读数。</Text>
        </Space>
      )}
    </Space>
  );
}
