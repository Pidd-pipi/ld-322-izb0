import { Badge, Button, Empty, List, Tag, Tooltip, Typography } from 'antd';
import { CheckCircleTwoTone, ClockCircleTwoTone, FileTextOutlined } from '@ant-design/icons';
import type { MeasurementBatch } from '../../types/domain';

const { Text } = Typography;

interface Props {
  batches: MeasurementBatch[];
  selectedId?: number;
  onSelect: (id: number) => void;
}

export default function BatchList({ batches, selectedId, onSelect }: Props) {
  if (batches.length === 0) {
    return <Empty description="暂无批次，请先为温室创建复核批次" />;
  }
  return (
    <List
      dataSource={batches}
      renderItem={(batch) => {
        const failed = (batch.lastIssues?.length ?? 0) > 0 && batch.status === 'draft';
        return (
          <List.Item
            style={{
              cursor: 'pointer',
              padding: '12px 16px',
              background: selectedId === batch.id ? '#f0f9eb' : undefined,
              borderLeft: selectedId === batch.id ? '3px solid #52a352' : '3px solid transparent',
            }}
            onClick={() => onSelect(batch.id)}
          >
            <List.Item.Meta
              avatar={
                batch.status === 'archived' ? (
                  <CheckCircleTwoTone twoToneColor="#52c41a" style={{ fontSize: 22 }} />
                ) : (
                  <Badge dot={failed}>
                    <ClockCircleTwoTone twoToneColor={failed ? '#fa8c16' : '#8c8c8c'} style={{ fontSize: 22 }} />
                  </Badge>
                )
              }
              title={
                <span>
                  <Text strong>{batch.batchNo}</Text>{' '}
                  {batch.status === 'archived' ? (
                    <Tag color="success">已归档</Tag>
                  ) : (
                    <Tag color="processing">待提交</Tag>
                  )}
                  {failed && <Tag color="error">校验失败 {batch.lastIssues?.length} 项</Tag>}
                </span>
              }
              description={
                <span>
                  <Text type="secondary">{batch.greenhouse?.name ?? `温室 #${batch.greenhouseId}`}</Text>
                  <br />
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    已录入 {batch.entries?.length ?? 0} 项
                    {batch.submittedAt ? ` · 归档于 ${new Date(batch.submittedAt).toLocaleString()}` : ''}
                  </Text>
                </span>
              }
            />
            <Tooltip title="查看详情">
              <Button size="small" type="text" icon={<FileTextOutlined />} />
            </Tooltip>
          </List.Item>
        );
      }}
    />
  );
}
